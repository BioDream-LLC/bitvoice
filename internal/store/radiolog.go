package store

import (
	"encoding/json"
	"strings"
	"time"
)

// Transmission is one transmission on a radio console channel: a radio heard (rx) or a dispatcher
// sending (tx), with what is known of who and how, and its recording.
type Transmission struct {
	ID          string    `json:"id"`
	Channel     string    `json:"channel"`
	ChannelName string    `json:"channelName"`
	Direction   string    `json:"direction"` // rx (heard) or tx (sent)
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Seconds     float64   `json:"seconds"`
	Unit        string    `json:"unit,omitempty"`       // the radio's unit, when the system says (P25)
	UnitName    string    `json:"unitName,omitempty"`   // its alias on the console
	Dispatcher  string    `json:"dispatcher,omitempty"` // the extension that sent it
	Tone        string    `json:"tone,omitempty"`       // an alert tone, when that is what was sent
	Emergency   bool      `json:"emergency,omitempty"`
	Encryption  string    `json:"encryption,omitempty"` // AES-256 and so on; empty when clear
	Private     bool      `json:"private,omitempty"`    // a unit-to-unit call
	Recording   string    `json:"recording,omitempty"`
}

// AddTransmission saves a transmission to the radio log.
func (s *Store) AddTransmission(t Transmission) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(s.ctx(), `INSERT INTO radio_log (id, start_ms, channel, unit, body) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET body = excluded.body`, t.ID, t.Start.UnixMilli(), t.Channel, t.Unit, string(b))
	return err
}

// RadioLogQuery filters the radio log.
type RadioLogQuery struct {
	Since, Until time.Time
	Channel      string
	Search       string // matches unit, dispatcher, channel name
	Limit        int
}

// RadioLog returns matching transmissions, newest first.
func (s *Store) RadioLog(q RadioLogQuery) ([]Transmission, error) {
	if q.Limit <= 0 || q.Limit > 5000 {
		q.Limit = 500
	}
	where, args := []string{"1 = 1"}, []any{}
	if !q.Since.IsZero() {
		where, args = append(where, "start_ms >= ?"), append(args, q.Since.UnixMilli())
	}
	if !q.Until.IsZero() {
		where, args = append(where, "start_ms <= ?"), append(args, q.Until.UnixMilli())
	}
	if q.Channel != "" {
		where, args = append(where, "channel = ?"), append(args, q.Channel)
	}
	rows, err := s.db.Query(s.ctx(), `SELECT body FROM radio_log WHERE `+strings.Join(where, " AND ")+` ORDER BY start_ms DESC, id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	search := strings.ToLower(strings.TrimSpace(q.Search))
	out := []Transmission{}
	for rows.Next() && len(out) < q.Limit {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var t Transmission
		if json.Unmarshal([]byte(body), &t) != nil {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(t.Unit+" "+t.Dispatcher+" "+t.ChannelName+" "+t.Tone), search) {
			continue
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
