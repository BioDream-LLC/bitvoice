package compat

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/pbx"
)

// fullPBX is the fake with the engine's optional abilities: agents, parking, rooms, mailboxes, recording.
type fullPBX struct {
	*fakePBX
	agents map[string]pbx.Presence
	rooms  []pbx.ConferenceRoom
	paused map[string]bool
	acts   []string
}

func (f *fullPBX) SetAgent(ext string, out, paused bool, reason string) pbx.Presence {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := pbx.Presence{Extension: ext, LoggedOut: out, Paused: paused, Reason: reason, Since: time.Now()}
	f.agents[ext] = p
	return p
}
func (f *fullPBX) Agents() []pbx.Presence {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []pbx.Presence
	for _, p := range f.agents {
		out = append(out, p)
	}
	return out
}
func (f *fullPBX) Parked() []pbx.ParkedCall {
	return []pbx.ParkedCall{{Slot: "71", CallID: "parkedcall01", Party: "2125550100", ParkedBy: "100", Since: time.Now().Add(-30 * time.Second)}}
}
func (f *fullPBX) Conferences() []pbx.ConferenceRoom {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]pbx.ConferenceRoom(nil), f.rooms...)
}
func (f *fullPBX) MuteConferenceMember(n, id string, muted bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.rooms {
		for j := range f.rooms[i].Members {
			if f.rooms[i].Number == n && f.rooms[i].Members[j].ID == id {
				f.rooms[i].Members[j].Muted = muted
				return nil
			}
		}
	}
	return errors.New("no such member")
}
func (f *fullPBX) RemoveConferenceMember(n, id string) error {
	f.mu.Lock()
	f.acts = append(f.acts, "kick "+n+" "+id)
	f.mu.Unlock()
	return nil
}
func (f *fullPBX) LockConference(n string, locked bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rooms[0].Locked = locked
	return nil
}
func (f *fullPBX) MailboxCounts(box string) (int, int, bool) {
	if box != "100" {
		return 0, 0, false
	}
	return 2, 5, true
}
func (f *fullPBX) PauseRecording(id string, pause bool, by string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused[id] = pause
	return id == "abcdef123456"
}

func TestAMIMoreActions(t *testing.T) {
	f := &fullPBX{fakePBX: newFake(), agents: map[string]pbx.Presence{}, paused: map[string]bool{},
		rooms: []pbx.ConferenceRoom{{Number: "800", Name: "Board", Members: []pbx.ConferenceMember{
			{ID: "m1aaaaaaaaaa", Number: "100", Name: "Reception", Leader: true, Joined: time.Now()},
			{ID: "m2bbbbbbbbbb", Number: "101", Name: "Dr Lee", Joined: time.Now()}}}}}
	bus, a := events0(t, f)
	c := dialAMI(t, a)
	if m := c.do("Action", "Login", "Username", "crm", "Secret", "a long crm secret"); m.get("Response") != "Success" {
		t.Fatal(m)
	}

	// Agents.
	if m := c.do("Action", "QueuePause", "Interface", "Local/101@from-queue/n", "Paused", "true", "Reason", "lunch"); m.get("Response") != "Success" {
		t.Fatal(m)
	}
	if p := f.agents["101"]; !p.Paused || p.Reason != "lunch" || p.LoggedOut {
		t.Fatalf("%+v", p)
	}
	c.do("Action", "QueueRemove", "Interface", "PJSIP/101", "Queue", "700")
	if !f.agents["101"].LoggedOut {
		t.Fatal("not signed out")
	}
	c.do("Action", "QueueAdd", "Interface", "PJSIP/101", "Queue", "700")
	if f.agents["101"].LoggedOut {
		t.Fatal("not signed in")
	}
	if m := c.do("Action", "QueuePause", "Interface", "PJSIP/999", "Paused", "true"); m.get("Response") != "Error" {
		t.Fatal(m)
	}

	// Lists: each is a start, its items, and a Complete with the count.
	items := func(complete string) []amiMsg {
		var out []amiMsg
		for {
			m := c.next()
			if m.get("Event") == complete {
				return out
			}
			if m.get("Event") != "" {
				out = append(out, m)
			}
		}
	}
	c.do("Action", "ParkedCalls")
	if ps := items("ParkedCallsComplete"); len(ps) != 1 || ps[0].get("ParkingSpace") != "71" || ps[0].get("ParkeeCallerIDNum") != "2125550100" {
		t.Fatalf("%v", ps)
	}
	c.do("Action", "ConfbridgeListRooms")
	if rs := items("ConfbridgeListRoomsComplete"); len(rs) != 1 || rs[0].get("Parties") != "2" {
		t.Fatalf("%v", rs)
	}
	c.do("Action", "ConfbridgeList", "Conference", "800")
	ms := items("ConfbridgeListComplete")
	if len(ms) != 2 || ms[0].get("Admin") != "Yes" || !strings.HasPrefix(ms[1].get("Channel"), "PJSIP/101-") {
		t.Fatalf("%v", ms)
	}
	if m := c.do("Action", "ConfbridgeMute", "Conference", "800", "Channel", ms[1].get("Channel")); m.get("Response") != "Success" || !f.rooms[0].Members[1].Muted {
		t.Fatalf("mute %v", m)
	}
	if m := c.do("Action", "ConfbridgeKick", "Conference", "800", "Channel", "PJSIP/100"); m.get("Response") != "Success" || f.acts[0] != "kick 800 m1aaaaaaaaaa" {
		t.Fatalf("kick %v %v", m, f.acts)
	}
	c.do("Action", "ConfbridgeLock", "Conference", "Board")
	if !f.rooms[0].Locked {
		t.Fatal("not locked")
	}
	if m := c.do("Action", "MailboxCount", "Mailbox", "100@default"); m.get("NewMessages") != "2" || m.get("OldMessages") != "5" {
		t.Fatal(m)
	}
	if m := c.do("Action", "MailboxStatus", "Mailbox", "999"); m.get("Response") != "Error" {
		t.Fatal(m)
	}
	if m := c.do("Action", "PauseMonitor", "Channel", "PJSIP/100-abcdef121"); m.get("Response") != "Success" || !f.paused["abcdef123456"] {
		t.Fatal(m)
	}
	if m := c.do("Action", "Getvar", "Channel", "PJSIP/100-abcdef121", "Variable", "CALLERID(num)"); m.get("Value") != "100" {
		t.Fatal(m)
	}
	if m := c.do("Action", "CoreStatus"); m.get("CoreCurrentCalls") != "2" {
		t.Fatal(m)
	}
	if m := c.do("Action", "Command", "Command", "core show channels"); m.get("Response") != "Error" {
		t.Fatal(m)
	}
	c.do("Action", "ExtensionStateList")
	if es := items("ExtensionStateListComplete"); len(es) == 0 {
		t.Fatal("no extension states")
	}

	// A read-only login cannot pause agents or kick members.
	ro := dialAMI(t, a)
	ro.do("Action", "Login", "Username", "board", "Secret", "a long board secret")
	if m := ro.do("Action", "QueuePause", "Interface", "PJSIP/101", "Paused", "false"); m.get("Response") != "Error" {
		t.Fatal(m)
	}

	// Filters: only Newchannel passes; a UserEvent from the other client is dropped, then let through.
	if m := c.do("Action", "Filter", "Operation", "Add", "Filter", "Event: Newchannel"); m.get("Response") != "Success" {
		t.Fatal(m)
	}
	ro.do("Action", "UserEvent", "UserEvent", "Wallboard", "Message", "hello")
	bus.Publish("call.started", pbx.CallInfo{ID: "filtered0001", State: "ringing", From: "101", To: "100", Start: time.Now()})
	if m := c.next(); m.get("Event") != "Newchannel" {
		t.Fatalf("the filter let through %v", m)
	}
	c.do("Action", "Filter", "Filter", "Event: UserEvent")
	ro.do("Action", "UserEvent", "UserEvent", "Wallboard", "Message", "again")
	if m := c.next(); m.get("Event") != "UserEvent" || m.get("UserEvent") != "Wallboard" || m.get("Message") != "again" {
		t.Fatalf("user event %v", m)
	}
	// Mailbox events.
	bus.Publish("voicemail.changed", map[string]any{"box": "100", "new": 1, "old": 0})
	c.do("Action", "Filter", "Filter", "Event: MessageWaiting")
	bus.Publish("voicemail.changed", map[string]any{"box": "100", "new": 3, "old": 1})
	if m := c.next(); m.get("Event") != "MessageWaiting" || m.get("New") != "3" || m.get("Waiting") != "yes" {
		t.Fatalf("mwi %v", m)
	}
}

func events0(t *testing.T, f *fullPBX) (*events.Bus, *AMI) {
	bus := events.New()
	a := &AMI{PBX: f, Bus: bus, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Started: time.Now()}
	a.Apply(f.cfg.System.Compat)
	t.Cleanup(a.Close)
	if a.Addr() == "" {
		t.Fatal("not listening")
	}
	return bus, a
}

// The engine has every ability AMI and ARI look for.
var (
	_ agentPBX      = (*pbx.Engine)(nil)
	_ parkPBX       = (*pbx.Engine)(nil)
	_ confPBX       = (*pbx.Engine)(nil)
	_ mailPBX       = (*pbx.Engine)(nil)
	_ recordPBX     = (*pbx.Engine)(nil)
	_ AppPBX        = (*pbx.Engine)(nil)
	_ MediaPBX      = (*pbx.Engine)(nil)
	_ PBX           = (*pbx.Engine)(nil)
	_ appChannel    = (*pbx.AppChannel)(nil)
	_ pbx.StasisHub = (*ARI)(nil)
)
