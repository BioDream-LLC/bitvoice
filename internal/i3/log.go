package i3

import (
	"fmt"
	"strings"
	"time"
)

// LogEvent is a NENA i3 log event (NENA-STA-010 Logging Service): the common fields and the ones
// the call events add. Extra holds the fields of other event types.
type LogEvent struct {
	Type       string         `json:"logEventType"`
	Timestamp  string         `json:"timestamp"`
	ElementID  string         `json:"elementId"`
	AgencyID   string         `json:"agencyId"`
	AgentID    string         `json:"agencyAgentId,omitempty"`
	Position   string         `json:"agencyPositionId,omitempty"`
	CallID     string         `json:"callId,omitempty"`
	IncidentID string         `json:"incidentId,omitempty"`
	CallIDSIP  string         `json:"callIdSip,omitempty"`
	Direction  string         `json:"direction,omitempty"`
	CallType   string         `json:"standardPrimaryCallType,omitempty"`
	To         string         `json:"to,omitempty"`
	From       string         `json:"from,omitempty"`
	Target     string         `json:"target,omitempty"` // CallTransferLogEvent
	Extra      map[string]any `json:"extension,omitempty"`
}

// NewEvent is an event of a type for an incident, stamped now.
func NewEvent(kind string, in Incident, now time.Time) LogEvent {
	return LogEvent{Type: kind, Timestamp: now.Format(time.RFC3339Nano), ElementID: in.Element, AgencyID: in.Agency,
		AgentID: in.Agent, Position: in.Position, CallID: urnID("callid", in.ID, in.Element),
		IncidentID: urnID("incidentid", in.ID, in.Element), Direction: "incoming", CallType: "emergency", From: in.From, To: in.Queue}
}

// ALI spill: the text block CAD systems read from a call-handling system, as legacy ALI arrived
// on a serial line. There is no one national layout; this is the common one (a header line, the
// callback number and time, class of service, the address lines, the position) framed by STX and
// ETX, which most CAD interfaces take or can be set to take. Lines are 32 characters at most.

// ALI is one spill.
type ALI struct {
	Position string
	Callback string
	Time     time.Time
	Class    string // class of service: RESD, BUSN, WRLS, VOIP...
	Name     string
	Location *Location
	Provider string
}

const stx, etx = "\x02", "\x03"

func clip(s string, n int) string {
	s = strings.ToUpper(strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ", "\x02", "", "\x03", "").Replace(s)))
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Spill is the framed ALI text.
func (a ALI) Spill() []byte {
	var lines []string
	cb := a.Callback
	if d := strings.TrimPrefix(cb, "+1"); len(d) == 10 && cb != d {
		cb = "(" + d[:3] + ") " + d[3:6] + "-" + d[6:]
	}
	lines = append(lines, clip(fmt.Sprintf("POS %s  %s", firstNonEmpty(a.Position, "-"), a.Time.Format("01/02 15:04:05")), 32))
	lines = append(lines, clip(fmt.Sprintf("%s  %s", cb, firstNonEmpty(a.Class, "VOIP")), 32))
	if a.Name != "" {
		lines = append(lines, clip(a.Name, 32))
	}
	if l := a.Location; l != nil {
		if c := l.Civic; c != nil {
			lines = append(lines, clip(c.StreetLine(), 32))
			if extra := strings.TrimSpace(strings.Join([]string{join(" ", "BLDG", c.Building), join(" ", "UNIT", c.Unit), join(" ", "FLR", c.Floor), join(" ", "RM", c.Room), c.Location}, " ")); extra != "" {
				lines = append(lines, clip(extra, 32))
			}
			lines = append(lines, clip(strings.TrimSpace(c.City+" "+c.State+" "+c.PostalCode), 32))
		}
		if l.HasPoint() {
			lines = append(lines, clip(fmt.Sprintf("LAT %+.6f", *l.Lat), 32), clip(fmt.Sprintf("LON %+.6f", *l.Lon), 32))
			if l.Radius > 0 {
				lines = append(lines, clip(fmt.Sprintf("UNC %dM CONF %d%%", int(l.Radius+0.5), l.Confidence), 32))
			}
		}
	} else {
		lines = append(lines, "NO LOCATION RECEIVED")
	}
	if a.Provider != "" {
		lines = append(lines, clip("CO "+a.Provider, 32))
	}
	return []byte(stx + strings.Join(lines, "\r\n") + "\r\n" + etx)
}

// ClassOf guesses the class of service from Additional Data: wireless, VoIP, residence or business.
func ClassOf(a Additional) string {
	if a.Service != nil {
		for _, t := range a.Service.Types {
			switch strings.ToLower(t) {
			case "wireless":
				return "WRLS"
			case "voip", "nomadic":
				return "VOIP"
			case "pots", "coin":
				if strings.EqualFold(a.Service.Environment, "business") {
					return "BUSN"
				}
				return "RESD"
			}
		}
		if strings.EqualFold(a.Service.Environment, "business") {
			return "BUSN"
		}
	}
	return "VOIP"
}
