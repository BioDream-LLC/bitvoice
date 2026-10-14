package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/pms"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// hotelLink is the running link to the hotel system, restarted when its settings change.
type hotelLink struct {
	mu     sync.Mutex
	key    string
	link   *pms.Link
	cancel context.CancelFunc
}

// pmsHandler applies what the hotel system says to the engine and the wake-up book.
type pmsHandler struct{ s *Server }

func (h pmsHandler) CheckIn(room, name, account, lang string) {
	if err := h.s.Engine.CheckIn(pbx.Guest{Room: room, Name: name, Account: account, Language: lang}); err != nil {
		h.s.Log.Warn("hotel system checked a guest in to a room that is not a guest room", "room", room)
	}
}
func (h pmsHandler) CheckOut(room string) { _ = h.s.Engine.CheckOut(room) }
func (h pmsHandler) WakeSet(room string, at time.Time) {
	if _, err := h.s.Store.PutWakeUp(store.WakeUp{Extension: room, At: at, By: "hotel system"}); err == nil {
		h.s.Bus.Publish("wakeup.changed", map[string]string{"extension": room})
	}
}
func (h pmsHandler) WakeClear(room string) {
	_ = h.s.Store.DeleteWakeUp(room)
	h.s.Bus.Publish("wakeup.changed", map[string]string{"extension": room})
}

// RunHotel keeps the hotel system link in line with the settings, and charges guests' calls.
func (s *Server) RunHotel(ctx context.Context) {
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)
	s.syncHotelLink(ctx)
	for {
		select {
		case <-ctx.Done():
			s.hotel.mu.Lock()
			if s.hotel.cancel != nil {
				s.hotel.cancel()
			}
			s.hotel.mu.Unlock()
			return
		case ev := <-sub.C:
			switch ev.Type {
			case "config.applied":
				s.syncHotelLink(ctx)
			case "call.logged":
				if r, ok := ev.Data.(store.CDR); ok {
					s.chargeGuest(r)
				}
			}
		}
	}
}

func (s *Server) syncHotelLink(ctx context.Context) {
	cfg := s.config()
	pm := cfg.System.Hotel.PMS
	key := ""
	if pm.Kind == "fias" {
		key = net.JoinHostPort(pm.Host, strconv.Itoa(pm.Port)) + "|" + cfg.System.Timezone
	}
	s.hotel.mu.Lock()
	defer s.hotel.mu.Unlock()
	if key == s.hotel.key {
		return
	}
	if s.hotel.cancel != nil {
		s.hotel.cancel()
	}
	s.hotel.key, s.hotel.link, s.hotel.cancel = key, nil, nil
	if key == "" {
		return
	}
	lctx, cancel := context.WithCancel(ctx)
	l := &pms.Link{Addr: net.JoinHostPort(pm.Host, strconv.Itoa(pm.Port)), Handler: pmsHandler{s}, Log: s.Log, Loc: cfg.System.Location()}
	s.hotel.link, s.hotel.cancel = l, cancel
	go l.Run(lctx)
}

func (s *Server) pmsLink() *pms.Link {
	s.hotel.mu.Lock()
	defer s.hotel.mu.Unlock()
	return s.hotel.link
}

// pmsEvent tells the hotel system what happened on the phones.
func (s *Server) pmsEvent(kind string, data map[string]string) {
	l := s.pmsLink()
	if l == nil {
		return
	}
	at, _ := time.Parse(time.RFC3339, data["at"])
	switch kind {
	case "wakeup.set":
		l.WakeSet(data["room"], at)
	case "wakeup.result":
		if w, ok := s.Store.WakeUpFor(data["room"]); ok {
			at = w.At
		}
		l.WakeResult(data["room"], at, data["outcome"] == "answered")
	case "roomstatus":
		l.RoomStatus(data["room"], data["status"])
	}
}

// chargeGuest posts an answered outside call from a checked-in room to the guest's bill.
func (s *Server) chargeGuest(r store.CDR) {
	cfg := s.config()
	pm := cfg.System.Hotel.PMS
	if !pm.ChargeCalls || r.Direction != "outbound" || r.Billsec == 0 || r.Ext == "" {
		return
	}
	x := cfg.FindExtension(r.Ext)
	g, in := s.Engine.GuestIn(r.Ext)
	if x == nil || !x.GuestRoom || !in {
		return
	}
	cents := int64(r.Cost*100*float64(100+pm.Markup)/100 + 0.5)
	switch pm.Kind {
	case "fias":
		if l := s.pmsLink(); l != nil {
			l.Charge(r.Ext, r.To, cents, time.Duration(r.Billsec)*time.Second, r.Start)
		}
	case "api":
		if pm.ChargeURL == "" {
			return
		}
		body, _ := json.Marshal(map[string]any{"room": r.Ext, "account": g.Account, "guest": g.Name, "number": r.To,
			"seconds": r.Billsec, "cents": cents, "start": r.Start, "callId": r.ID})
		go func() {
			req, err := http.NewRequest(http.MethodPost, pm.ChargeURL, bytes.NewReader(body))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := crmHTTP.Do(req)
			if err != nil {
				s.Log.Warn("hotel charge not sent", "room", r.Ext, "err", err)
				return
			}
			resp.Body.Close()
		}()
	}
}

// RoomView is a guest room on the hotel screen.
type RoomView struct {
	Extension string        `json:"extension"`
	Name      string        `json:"name"`
	Guest     *pbx.Guest    `json:"guest,omitempty"`
	Status    string        `json:"status,omitempty"`
	StatusAt  time.Time     `json:"statusAt,omitzero"`
	WakeUp    *store.WakeUp `json:"wakeUp,omitempty"`
}

func (s *Server) hotelRoutes() {
	s.handle("GET /api/hotel", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cfg, _ := s.Store.Config()
		guests := map[string]pbx.Guest{}
		for _, g := range s.Engine.Guests() {
			guests[g.Room] = g
		}
		wakes := map[string]store.WakeUp{}
		for _, wu := range s.Store.WakeUps() {
			wakes[wu.Extension] = wu
		}
		rooms := []RoomView{}
		for _, x := range cfg.Extensions {
			if !x.GuestRoom {
				continue
			}
			v := RoomView{Extension: x.Number, Name: x.Name}
			if g, ok := guests[x.Number]; ok {
				v.Status, v.StatusAt = g.Status, g.StatusAt
				if !g.Since.IsZero() {
					v.Guest = &g
				}
			}
			if wu, ok := wakes[x.Number]; ok {
				v.WakeUp = &wu
			}
			rooms = append(rooms, v)
		}
		out := map[string]any{"rooms": rooms, "wakeUps": s.Store.WakeUps()}
		if l := s.pmsLink(); l != nil {
			out["link"] = l.Status()
		}
		writeJSON(w, 200, out)
	})
	room := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		cfg, _ := s.Store.Config()
		n := r.PathValue("room")
		if x := cfg.FindExtension(n); x == nil || !x.GuestRoom {
			writeError(w, http.StatusNotFound, n+" is not a guest room: switch on Guest room on its extension")
			return "", false
		}
		return n, true
	}
	s.handle("POST /api/hotel/rooms/{room}/checkin", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		n, ok := room(w, r)
		if !ok {
			return
		}
		var in struct {
			Name     string `json:"name"`
			Account  string `json:"account"`
			Language string `json:"language"`
		}
		if err := readJSON(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, "send name, account and language")
			return
		}
		if err := s.Engine.CheckIn(pbx.Guest{Room: n, Name: in.Name, Account: in.Account, Language: in.Language}); err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "hotel.checkin", n)
		w.WriteHeader(http.StatusNoContent)
	})
	s.handle("POST /api/hotel/rooms/{room}/checkout", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		n, ok := room(w, r)
		if !ok {
			return
		}
		if err := s.Engine.CheckOut(n); err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "hotel.checkout", n)
		w.WriteHeader(http.StatusNoContent)
	})
	s.handle("PUT /api/hotel/rooms/{room}/status", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		n, ok := room(w, r)
		if !ok {
			return
		}
		var in struct {
			Status string `json:"status"`
		}
		if readJSON(r, &in) != nil || len(in.Status) != 1 || in.Status[0] < '1' || in.Status[0] > '9' {
			writeError(w, http.StatusUnprocessableEntity, "a room status is a digit 1 to 9")
			return
		}
		s.Engine.SetRoomStatus(n, in.Status)
		w.WriteHeader(http.StatusNoContent)
	})
	// Wake-up calls work on any extension, guest room or not (night shifts, on-call staff).
	s.handle("GET /api/wakeups", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, map[string]any{"items": s.Store.WakeUps()})
	})
	s.handle("PUT /api/wakeups/{ext}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cfg, _ := s.Store.Config()
		ext := r.PathValue("ext")
		if cfg.FindExtension(ext) == nil {
			writeError(w, http.StatusNotFound, "no extension "+ext)
			return
		}
		var in struct {
			At    time.Time `json:"at"`
			Daily bool      `json:"daily"`
		}
		if err := readJSON(r, &in); err != nil || in.At.IsZero() {
			writeErr(w, config.Problems{{Field: "at", Message: "choose when"}})
			return
		}
		if in.At.Before(time.Now().Add(-time.Minute)) && !in.Daily {
			writeErr(w, config.Problems{{Field: "at", Message: "that time has passed"}})
			return
		}
		wu, err := s.Store.PutWakeUp(store.WakeUp{Extension: ext, At: in.At, Daily: in.Daily, By: u.Username})
		if err != nil {
			writeErr(w, err)
			return
		}
		s.Bus.Publish("wakeup.changed", map[string]string{"extension": ext})
		s.pmsEvent("wakeup.set", map[string]string{"room": ext, "at": in.At.Format(time.RFC3339)})
		s.audit(r, u.Username, "wakeup.set", ext)
		writeJSON(w, 200, wu)
	})
	s.handle("DELETE /api/wakeups/{ext}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if err := s.Store.DeleteWakeUp(r.PathValue("ext")); err != nil {
			writeErr(w, err)
			return
		}
		s.Bus.Publish("wakeup.changed", map[string]string{"extension": r.PathValue("ext")})
		s.audit(r, u.Username, "wakeup.cancel", r.PathValue("ext"))
		w.WriteHeader(http.StatusNoContent)
	})
}
