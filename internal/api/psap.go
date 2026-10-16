package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/i3"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// The 911 profile's outputs: when a call-taker answers a 911 call, CAD gets the ALI spill and the
// EIDO, and the NENA i3 log gets its events (DATA/i3log/YYYY-MM-DD.jsonl).

func (s *Server) i3Dir() string { return filepath.Join(s.DataDir, "i3log") }

func elementID(cfg config.Config) string {
	if e := cfg.System.PSAP.ElementID; e != "" {
		return e
	}
	h, _ := os.Hostname()
	if h == "" {
		h = "bitvoice"
	}
	return h
}

func agencyID(cfg config.Config) string {
	if a := cfg.System.PSAP.AgencyID; a != "" {
		return a
	}
	return "psap.invalid"
}

// incidentOf turns an answered call into what EIDO and the log need.
func incidentOf(cfg config.Config, a pbx.EmergencyAnswer) i3.Incident {
	in := i3.Incident{ID: a.Call.ID, Agency: agencyID(cfg), Element: elementID(cfg), Queue: a.Call.To, From: a.Call.From,
		CallerName: a.Call.FromName, Agent: a.Agent, Position: a.Agent, Start: a.Call.Start, Answer: a.Call.Answer,
		LocationRaw: a.PIDF}
	if x := cfg.FindExtension(a.Agent); x != nil && x.Email != "" {
		in.Agent = x.Email // an agent is named by an address in i3
	}
	if em := a.Call.Emergency; em != nil {
		in.Location, in.Additional = em.Location, em.Additional
		if len(in.LocationRaw) == 0 {
			in.LocationRaw = em.PIDF
		}
	}
	return in
}

// EmergencyAnswered is the engine's hook: CAD outputs and the log, each on its own, so a CAD that
// is down never holds up the call-taker.
func (s *Server) EmergencyAnswered(a pbx.EmergencyAnswer) {
	cfg := s.config()
	in := incidentOf(cfg, a)
	now := time.Now()
	ans := i3.NewEvent("CallStateChangeLogEvent", in, now)
	ans.Extra = map[string]any{"state": "answered"}
	s.logI3(ans)
	ps := cfg.System.PSAP
	if ps.ALIAddress != "" {
		go func() {
			err := sendALI(ps.ALIAddress, aliOf(a))
			s.cadResult("ALI", in, err)
		}()
	}
	if ps.EIDOURL != "" {
		s.withMedia(cfg, &in, a.Call)
		go func() {
			doc, err := i3.EIDO(in, time.Now())
			if err == nil {
				err = postEIDO(ps.EIDOURL, ps.EIDOToken, doc)
			}
			ev := i3.NewEvent("EidoLogEvent", in, time.Now())
			ev.Extra = map[string]any{"direction": "outgoing", "destination": ps.EIDOURL, "ok": err == nil}
			s.logI3(ev)
			s.cadResult("EIDO", in, err)
		}()
	}
	if s.Bus != nil {
		s.Bus.Publish("emergency.answered", map[string]any{"call": a.Call.ID, "agent": a.Agent, "from": a.Call.From})
	}
}

func aliOf(a pbx.EmergencyAnswer) i3.ALI {
	al := i3.ALI{Position: a.Agent, Callback: a.Call.From, Time: a.Call.Answer, Name: a.Call.FromName}
	if em := a.Call.Emergency; em != nil {
		al.Location = em.Location
		al.Class = i3.ClassOf(em.Additional)
		if len(em.Additional.Providers) > 0 {
			al.Provider = em.Additional.Providers[0].Name
		}
		if em.Additional.Subscriber != nil && em.Additional.Subscriber.Name != "" {
			al.Name = em.Additional.Subscriber.Name
		}
	}
	return al
}

func (s *Server) cadResult(what string, in i3.Incident, err error) {
	if err != nil {
		s.Log.Warn("911: CAD did not take the "+what, "call", in.ID, "err", err)
		if s.Bus != nil {
			s.Bus.Publish("emergency.cad_failed", map[string]string{"call": in.ID, "what": what, "error": err.Error()})
		}
		return
	}
	s.Log.Info("911: "+what+" sent to CAD", "call", in.ID)
}

// sendALI writes the spill to CAD's listener.
func sendALI(addr string, a i3.ALI) error {
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = c.Write(a.Spill())
	return err
}

func postEIDO(url, token string, doc []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(doc))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/emergency.eido+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("CAD said %s", resp.Status)
	}
	return nil
}

// logI3 appends events to today's i3 log.
func (s *Server) logI3(evs ...i3.LogEvent) {
	if s.DataDir == "" {
		return
	}
	s.i3Mu.Lock()
	defer s.i3Mu.Unlock()
	if err := os.MkdirAll(s.i3Dir(), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(s.i3Dir(), time.Now().UTC().Format("2006-01-02")+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		s.Log.Error("911: cannot write the i3 log", "err", err)
		return
	}
	defer f.Close()
	for _, ev := range evs {
		b, _ := json.Marshal(ev)
		f.Write(append(b, '\n'))
	}
}

// i3Events reads the log of one UTC day, newest first.
func (s *Server) i3Events(day string) []map[string]any {
	f, err := os.Open(filepath.Join(s.i3Dir(), day+".jsonl"))
	if err != nil {
		return []map[string]any{}
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			out = append(out, m)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out
}

func (s *Server) psapRoutes() {
	s.handle("GET /api/psap/log", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		day := r.URL.Query().Get("day")
		if _, err := time.Parse("2006-01-02", day); err != nil {
			day = time.Now().UTC().Format("2006-01-02")
		}
		files, _ := filepath.Glob(filepath.Join(s.i3Dir(), "*.jsonl"))
		var days []string
		for _, f := range files {
			days = append(days, strings.TrimSuffix(filepath.Base(f), ".jsonl"))
		}
		sort.Sort(sort.Reverse(sort.StringSlice(days)))
		if days == nil {
			days = []string{}
		}
		writeJSON(w, 200, map[string]any{"day": day, "days": days, "items": s.i3Events(day)})
	})
	s.handle("POST /api/calls/{id}/rebid", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		loc, err := s.Engine.Rebid(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, 409, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"location": loc})
	})
	s.handle("POST /api/calls/{id}/text", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var b struct {
			Via  string `json:"via"`
			Text string `json:"text"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&b); err != nil {
			writeError(w, 400, "send {\"text\": ...}")
			return
		}
		switch b.Via {
		case "", "tty", "rtt", "msrp", "sip":
		default:
			writeError(w, 400, "via is tty, rtt, msrp or sip")
			return
		}
		l, err := s.Engine.SendCallText(r.PathValue("id"), b.Via, b.Text, "", firstNonEmpty(u.Name, u.Username))
		if err != nil {
			writeError(w, 409, err.Error())
			return
		}
		writeJSON(w, 200, l)
	})
	s.handle("POST /api/calls/{id}/take", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if err := s.Engine.TakeTextCall(r.PathValue("id"), u.Username, firstNonEmpty(u.Name, u.Username)); err != nil {
			writeError(w, 409, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"taken": true})
	})
	s.handle("POST /api/calls/{id}/end-text", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if err := s.Engine.EndTextCall(r.PathValue("id")); err != nil {
			writeError(w, 409, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ended": true})
	})
	s.handle("GET /api/calls/{id}/eido", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		for _, c := range s.Engine.Calls() {
			if c.ID == r.PathValue("id") && c.Emergency != nil {
				cfg := s.config()
				in := incidentOf(cfg, pbx.EmergencyAnswer{Call: c, Agent: c.Emergency.TakenBy})
				s.withMedia(cfg, &in, c)
				doc, err := i3.EIDO(in, time.Now())
				if err != nil {
					writeError(w, 500, err.Error())
					return
				}
				w.Header().Set("Content-Type", "application/emergency.eido+json")
				w.Write(doc)
				return
			}
		}
		writeError(w, 404, "no 911 call with that ID now")
	})
}

// AnswerReport is how fast 911 calls were answered over a window, as state and NENA reports ask
// (NENA-STA-020: 90% within 15 seconds, 95% within 20).
type AnswerReport struct {
	From, To     time.Time
	Target       int            `json:"target"`
	Calls        int            `json:"calls"`
	Answered     int            `json:"answered"`
	Abandoned    int            `json:"abandoned"`
	WithinTarget float64        `json:"withinTarget"` // percent of answered calls
	Within20     float64        `json:"within20"`
	Average      float64        `json:"average"` // seconds to answer
	Longest      float64        `json:"longest"`
	Buckets      []int          `json:"buckets"` // answered within 0-10, 10-15, 15-20, 20-40, 40+ seconds
	ByTaker      []TakerAnswers `json:"byTaker"`
	ByHour       [24]int        `json:"byHour"`
}

type TakerAnswers struct {
	Ext     string  `json:"ext"`
	Name    string  `json:"name"`
	Calls   int     `json:"calls"`
	Average float64 `json:"average"`
}

func answerReport(cfg config.Config, cdrs []store.CDR, from, to time.Time) AnswerReport {
	target := cfg.System.PSAP.AnswerTarget
	if target == 0 {
		target = 15
	}
	r := AnswerReport{From: from, To: to, Target: target, Buckets: make([]int, 5), ByTaker: []TakerAnswers{}}
	loc := cfg.System.Location()
	takers := map[string]*TakerAnswers{}
	var sum float64
	within, within20 := 0, 0
	for _, c := range cdrs {
		if !c.Emergency {
			continue
		}
		r.Calls++
		r.ByHour[c.Start.In(loc).Hour()]++
		// The answer that counts is a person's: a queue that held the caller had not answered them.
		answered := c.Taken
		if answered.IsZero() && c.Emergency && c.AnsweredExt != "" {
			answered = c.Answer
		}
		if answered.IsZero() {
			r.Abandoned++
			continue
		}
		r.Answered++
		secs := answered.Sub(c.Start).Seconds()
		sum += secs
		r.Longest = max(r.Longest, secs)
		if secs <= float64(target) {
			within++
		}
		if secs <= 20 {
			within20++
		}
		switch {
		case secs <= 10:
			r.Buckets[0]++
		case secs <= 15:
			r.Buckets[1]++
		case secs <= 20:
			r.Buckets[2]++
		case secs <= 40:
			r.Buckets[3]++
		default:
			r.Buckets[4]++
		}
		ext := c.AnsweredExt
		t := takers[ext]
		if t == nil {
			t = &TakerAnswers{Ext: ext}
			if x := cfg.FindExtension(ext); x != nil {
				t.Name = x.Name
			}
			takers[ext] = t
		}
		t.Average = (t.Average*float64(t.Calls) + secs) / float64(t.Calls+1)
		t.Calls++
	}
	if r.Answered > 0 {
		r.Average = sum / float64(r.Answered)
		r.WithinTarget = 100 * float64(within) / float64(r.Answered)
		r.Within20 = 100 * float64(within20) / float64(r.Answered)
	}
	for _, t := range takers {
		r.ByTaker = append(r.ByTaker, *t)
	}
	sort.Slice(r.ByTaker, func(i, j int) bool { return r.ByTaker[i].Calls > r.ByTaker[j].Calls })
	return r
}

func (s *Server) psapReportRoutes() {
	s.handle("GET /api/psap/report", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cfg := s.config()
		loc := cfg.System.Location()
		day := func(k string, def time.Time) time.Time {
			if t, err := time.ParseInLocation("2006-01-02", r.URL.Query().Get(k), loc); err == nil {
				return t
			}
			return def
		}
		today := time.Now().In(loc)
		from := day("from", time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -6))
		to := day("to", time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)).AddDate(0, 0, 1)
		cdrs, err := s.Store.CDRs(store.CDRQuery{Since: from, Until: to, Limit: 100000})
		if err != nil {
			writeErr(w, err)
			return
		}
		if r.URL.Query().Get("format") == "csv" {
			writeAnswerCSV(w, cfg, cdrs, from, to, r.URL.Query().Get("by"))
			return
		}
		writeJSON(w, 200, answerReport(cfg, cdrs, from, to))
	})
}

// writeAnswerCSV writes the answer-time report as a spreadsheet: one row a day (the counts state
// 911 offices ask for: calls, answered, abandoned, the share answered within the goal and within
// 20 seconds, the average and longest wait), or with by=call one row a 911 call.
func writeAnswerCSV(w http.ResponseWriter, cfg config.Config, cdrs []store.CDR, from, to time.Time, by string) {
	loc := cfg.System.Location()
	name := "911-answer-times-" + from.Format("2006-01-02") + "-to-" + to.AddDate(0, 0, -1).Format("2006-01-02")
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	cw := csv.NewWriter(w)
	if by == "call" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`-calls.csv"`)
		cw.Write([]string{"start", "callback number", "answered at", "seconds to answer", "call-taker", "abandoned", "text"})
		sort.Slice(cdrs, func(i, j int) bool { return cdrs[i].Start.Before(cdrs[j].Start) })
		for _, c := range cdrs {
			if !c.Emergency {
				continue
			}
			ans := c.Taken
			if ans.IsZero() && c.AnsweredExt != "" {
				ans = c.Answer
			}
			row := []string{c.Start.In(loc).Format("2006-01-02 15:04:05"), csvSafe(c.From), "", "", csvSafe(c.AnsweredExt), "yes", ""}
			if !ans.IsZero() {
				row[2], row[3], row[5] = ans.In(loc).Format("2006-01-02 15:04:05"), strconv.FormatFloat(ans.Sub(c.Start).Seconds(), 'f', 1, 64), "no"
			}
			if len(c.Text) > 0 {
				row[6] = c.Text[0].Via
			}
			cw.Write(row)
		}
		cw.Flush()
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.csv"`)
	target := cfg.System.PSAP.AnswerTarget
	if target == 0 {
		target = 15
	}
	cw.Write([]string{"day", "calls", "answered", "abandoned", "answered within " + strconv.Itoa(target) + " s (%)", "answered within 20 s (%)", "average to answer (s)", "longest to answer (s)"})
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		next := d.AddDate(0, 0, 1)
		var day []store.CDR
		for _, c := range cdrs {
			if !c.Start.Before(d) && c.Start.Before(next) {
				day = append(day, c)
			}
		}
		r := answerReport(cfg, day, d, next)
		f := func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
		cw.Write([]string{d.In(loc).Format("2006-01-02"), strconv.Itoa(r.Calls), strconv.Itoa(r.Answered), strconv.Itoa(r.Abandoned), f(r.WithinTarget), f(r.Within20), f(r.Average), f(r.Longest)})
	}
	cw.Flush()
}

// RunPSAPLog writes the i3 log's call start and end for every 911 call, answered or not (an
// abandoned call is logged too).
func (s *Server) RunPSAPLog(ctx context.Context) {
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.C:
			if ev.Type == "call.media" || ev.Type == "call.video" {
				s.eidoUpdate(ev)
				continue
			}
			if r, ok := ev.Data.(pbx.Route); ok {
				cfg := s.config()
				le := i3.LogEvent{Type: "RouteLogEvent", Timestamp: r.At.Format(time.RFC3339Nano), ElementID: elementID(cfg), AgencyID: agencyID(cfg),
					CallID: "urn:emergency:uid:callid:" + r.Call + ":" + elementID(cfg),
					Extra:  map[string]any{"recipientUri": r.PSAP, "ruleId": "location", "policyOwner": agencyID(cfg), "policyType": "OriginationPolicy", "why": r.Why, "service": r.Service}}
				s.logI3(le)
				continue
			}
			c, ok := ev.Data.(pbx.CallInfo)
			if !ok || c.Emergency == nil || (ev.Type != "call.started" && ev.Type != "call.ended") {
				continue
			}
			cfg := s.config()
			in := incidentOf(cfg, pbx.EmergencyAnswer{Call: c, Agent: c.Emergency.TakenBy})
			if ev.Type == "call.started" {
				s.logI3(i3.NewEvent("CallStartLogEvent", in, c.Start))
				if in.Location != nil {
					le := i3.NewEvent("LocationResponseLogEvent", in, time.Now())
					le.Extra = map[string]any{"location": in.Location}
					s.logI3(le)
				}
				continue
			}
			for _, l := range c.Emergency.Text { // the text conversation, one MessageLogEvent a turn
				le := i3.NewEvent("MessageLogEvent", in, l.At)
				le.Direction = map[string]string{"caller": "incoming", "taker": "outgoing"}[l.From]
				le.Extra = map[string]any{"text": l.Text, "via": l.Via}
				if l.From == "taker" {
					le.AgentID = l.Name
				}
				s.logI3(le)
			}
			end := i3.NewEvent("CallEndLogEvent", in, time.Now())
			if c.Emergency.TakenBy == "" {
				end.Extra = map[string]any{"abandoned": true}
			}
			s.logI3(end)
		}
	}
}

// withMedia names a call's photos, clips and live video in its EIDO, by links CAD opens with the
// call's viewing token. Without a viewing token, or an address the outside can reach, it adds none.
func (s *Server) withMedia(cfg config.Config, in *i3.Incident, c pbx.CallInfo) {
	if c.Emergency == nil || c.Emergency.Video == nil || c.Emergency.Video.ViewToken == "" {
		return
	}
	base := strings.TrimRight(cfg.System.PSAP.VideoLinkBase, "/")
	if base == "" {
		base = s.WebBase()
	}
	if base == "" {
		return
	}
	tok := c.Emergency.Video.ViewToken
	if c.Emergency.Video.Live {
		in.Media = append(in.Media, i3.MediaRef{ID: "live", URL: base + "/whep/" + tok, Purpose: "caller-video-live"})
	}
	ix, _ := s.readIndex(c.ID)
	for _, it := range ix.Items {
		purpose := "caller-photo"
		if strings.HasPrefix(it.Type, "video/") {
			purpose = "caller-video"
		}
		if it.Recording {
			purpose = "caller-video-recording"
		}
		in.Media = append(in.Media, i3.MediaRef{ID: strconv.Itoa(it.N), URL: base + "/m/" + tok + "/" + strconv.Itoa(it.N), Purpose: purpose})
	}
}

// eidoUpdate sends CAD the call's EIDO again when the caller's media changes (a photo arrives, live
// video starts or stops), once the call has been answered and so already sent.
func (s *Server) eidoUpdate(ev events.Event) {
	cfg := s.config()
	ps := cfg.System.PSAP
	if ps.EIDOURL == "" {
		return
	}
	var id string
	switch d := ev.Data.(type) {
	case map[string]any:
		id, _ = d["call"].(string)
	case pbx.CallInfo:
		id = d.ID
	}
	for _, c := range s.Engine.Calls() {
		if c.ID != id || c.Emergency == nil || c.Answer.IsZero() {
			continue
		}
		in := incidentOf(cfg, pbx.EmergencyAnswer{Call: c, Agent: c.Emergency.TakenBy})
		s.withMedia(cfg, &in, c)
		go func() {
			doc, err := i3.EIDO(in, time.Now())
			if err == nil {
				err = postEIDO(ps.EIDOURL, ps.EIDOToken, doc)
			}
			le := i3.NewEvent("EidoLogEvent", in, time.Now())
			le.Extra = map[string]any{"direction": "outgoing", "destination": ps.EIDOURL, "ok": err == nil, "why": "media"}
			s.logI3(le)
			s.cadResult("EIDO", in, err)
		}()
	}
}
