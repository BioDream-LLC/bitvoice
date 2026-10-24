package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// Conversation states.
const (
	ConvOpen   = "open"
	ConvClosed = "closed"
)

// InboxConv is one conversation in the messaging inbox.
type InboxConv struct {
	ID       string    `json:"id"`
	Channel  string    `json:"channel"` // webchat, whatsapp, messenger
	Contact  string    `json:"contact"` // the WhatsApp number, the Messenger user, the web chat visitor
	Name     string    `json:"name,omitempty"`
	Email    string    `json:"email,omitempty"`
	State    string    `json:"state"`
	Assignee string    `json:"assignee,omitempty"` // the user answering it
	Unread   int       `json:"unread"`
	Started  time.Time `json:"started"`
	Updated  time.Time `json:"updated"`
	LastIn   time.Time `json:"lastIn,omitzero"` // their last message: WhatsApp and Messenger allow replies for 24 hours after it
	Preview  string    `json:"preview"`
	Page     string    `json:"page,omitempty"` // web chat: the page it started on
	// TokenHash is a web chat visitor's key, hashed; never sent out.
	TokenHash string `json:"tokenHash,omitempty"`
}

// InboxMsg is one message.
type InboxMsg struct {
	ID     string    `json:"id"`
	Conv   string    `json:"conv"`
	At     time.Time `json:"at"`
	In     bool      `json:"in"` // from the contact
	Text   string    `json:"text"`
	By     string    `json:"by,omitempty"`     // the user who wrote it, or "auto"
	Status string    `json:"status,omitempty"` // out: sent or failed
	Error  string    `json:"error,omitempty"`
	ExtID  string    `json:"extId,omitempty"` // the channel's own ID
}

// ErrNoConv means there is no such conversation.
var ErrNoConv = errors.New("store: no such conversation")

// HashToken hashes a web chat visitor's key.
func HashToken(t string) string {
	s := sha256.Sum256([]byte(t))
	return hex.EncodeToString(s[:])
}

// PutConv saves a conversation.
func (s *Store) PutConv(c InboxConv) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(s.ctx(), `INSERT INTO inbox_convs (id, channel, contact, state, updated_ms, body) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET channel = excluded.channel, contact = excluded.contact, state = excluded.state,
		updated_ms = excluded.updated_ms, body = excluded.body`, c.ID, c.Channel, c.Contact, c.State, c.Updated.UnixMilli(), string(b))
	return err
}

func (s *Store) convRows(q string, args ...any) ([]InboxConv, error) {
	rows, err := s.db.Query(s.ctx(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InboxConv
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var c InboxConv
		if json.Unmarshal([]byte(body), &c) == nil {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

// Conv reads one conversation.
func (s *Store) Conv(id string) (InboxConv, error) {
	cs, err := s.convRows(`SELECT body FROM inbox_convs WHERE id = ?`, id)
	if err != nil {
		return InboxConv{}, err
	}
	if len(cs) == 0 {
		return InboxConv{}, ErrNoConv
	}
	return cs[0], nil
}

// ConvFor finds the open conversation with a contact on a channel, or "" when there is none.
func (s *Store) ConvFor(channel, contact string) (InboxConv, bool) {
	cs, err := s.convRows(`SELECT body FROM inbox_convs WHERE channel = ? AND contact = ? ORDER BY updated_ms DESC LIMIT 1`, channel, contact)
	if err != nil || len(cs) == 0 {
		return InboxConv{}, false
	}
	return cs[0], true
}

// Convs lists conversations, newest first: in a state ("" for all), at most limit.
func (s *Store) Convs(state string, limit int) ([]InboxConv, error) {
	if state != "" {
		return s.convRows(`SELECT body FROM inbox_convs WHERE state = ? ORDER BY updated_ms DESC LIMIT ?`, state, limit)
	}
	return s.convRows(`SELECT body FROM inbox_convs ORDER BY updated_ms DESC LIMIT ?`, limit)
}

// AddMsg stores a message and moves its conversation on (preview, time, unread count).
func (s *Store) AddMsg(m InboxMsg) (InboxMsg, error) {
	c, err := s.Conv(m.Conv)
	if err != nil {
		return m, err
	}
	if m.ID == "" {
		m.ID = config.NewID() + config.NewID()
	}
	if m.At.IsZero() {
		m.At = time.Now().UTC()
		// Messages are listed by time to the millisecond: one written in the same millisecond as the
		// one before it (a reply right after the question) goes a millisecond after it, never before.
		// The conversation's last message is the floor (its Updated may be older, written by a copy
		// of the conversation read before that message).
		last := c.Updated
		if ms, err := s.lastMsgAt(c.ID); err == nil && ms.After(last) {
			last = ms
		}
		if last = last.Truncate(time.Millisecond); !m.At.Truncate(time.Millisecond).After(last) {
			m.At = last.Add(time.Millisecond)
		}
	}
	b, _ := json.Marshal(m)
	if _, err := s.db.Exec(s.ctx(), `INSERT INTO inbox_msgs (id, conv, at_ms, body) VALUES (?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`,
		m.ID, m.Conv, m.At.UnixMilli(), string(b)); err != nil {
		return m, err
	}
	c.Updated = m.At
	c.Preview = m.Text
	if r := []rune(c.Preview); len(r) > 120 {
		c.Preview = string(r[:120])
	}
	if m.In {
		c.Unread++
		c.LastIn = m.At
		if c.State == ConvClosed {
			c.State = ConvOpen // a new message opens it again
		}
	}
	return m, s.PutConv(c)
}

// lastMsgAt is the time of a conversation's newest message (zero when it has none).
func (s *Store) lastMsgAt(conv string) (time.Time, error) {
	rows, err := s.db.Query(s.ctx(), `SELECT at_ms FROM inbox_msgs WHERE conv = ? ORDER BY at_ms DESC LIMIT 1`, conv)
	if err != nil {
		return time.Time{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return time.Time{}, rows.Err()
	}
	var ms int64
	if err := rows.Scan(&ms); err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(ms).UTC(), nil
}

// SetMsg updates a stored message (its sending status).
func (s *Store) SetMsg(m InboxMsg) error {
	b, _ := json.Marshal(m)
	_, err := s.db.Exec(s.ctx(), `UPDATE inbox_msgs SET body = ? WHERE id = ?`, string(b), m.ID)
	return err
}

// Msgs lists a conversation's messages, oldest first, after a time (zero for all), at most limit.
func (s *Store) Msgs(conv string, after time.Time, limit int) ([]InboxMsg, error) {
	rows, err := s.db.Query(s.ctx(), `SELECT body FROM inbox_msgs WHERE conv = ? AND at_ms > ? ORDER BY at_ms, id LIMIT ?`, conv, after.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InboxMsg
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var m InboxMsg
		if json.Unmarshal([]byte(body), &m) == nil {
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

// HasMsg says whether a channel's message was stored already (webhooks may come twice).
func (s *Store) HasMsg(conv, extID string) bool {
	ms, err := s.Msgs(conv, time.Time{}, 10000)
	if err != nil {
		return false
	}
	for _, m := range ms {
		if m.ExtID != "" && m.ExtID == extID {
			return true
		}
	}
	return false
}
