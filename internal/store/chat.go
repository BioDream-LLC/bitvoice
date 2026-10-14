package store

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"
)

// ChatMessage is one message in the staff portal's chat: between two people, or in a team room.
type ChatMessage struct {
	ID   string    `json:"id"`
	From string    `json:"from"` // extension
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// chatKeyRe is a conversation: d-<ext>-<ext> (two people, the lower number first) or r-<room id>.
var chatKeyRe = regexp.MustCompile(`^(d-[0-9]{2,8}-[0-9]{2,8}|r-[A-Za-z0-9_-]{1,30})$`)

// ErrChatKey is a conversation name that is not one.
var ErrChatKey = errors.New("store: not a conversation")

const maxChatMessages = 1000

var chatMu sync.Mutex

func chatDoc(key string) string { return "chat-" + key + ".json" }

// ChatMessages returns a conversation's messages, oldest first, at most limit of the newest (0: all
// kept), and only those after since when it is set.
func (s *Store) ChatMessages(key string, since time.Time, limit int) ([]ChatMessage, error) {
	if !chatKeyRe.MatchString(key) {
		return nil, ErrChatKey
	}
	chatMu.Lock()
	defer chatMu.Unlock()
	ms := s.loadChat(key)
	if !since.IsZero() {
		i, _ := slices.BinarySearchFunc(ms, since, func(m ChatMessage, t time.Time) int {
			if m.At.After(t) {
				return 1
			}
			return -1
		})
		ms = ms[i:]
	}
	if limit > 0 && len(ms) > limit {
		ms = ms[len(ms)-limit:]
	}
	return ms, nil
}

func (s *Store) loadChat(key string) []ChatMessage {
	ms := []ChatMessage{}
	_ = s.readDoc(chatDoc(key), &ms)
	if ms == nil {
		ms = []ChatMessage{}
	}
	return ms
}

// AddChatMessage keeps a message (the newest thousand of a conversation are kept, and none older
// than keepDays when it is set).
func (s *Store) AddChatMessage(key string, m ChatMessage, keepDays int) (ChatMessage, error) {
	if !chatKeyRe.MatchString(key) {
		return m, ErrChatKey
	}
	chatMu.Lock()
	defer chatMu.Unlock()
	ms := s.loadChat(key)
	if m.At.IsZero() {
		m.At = time.Now().UTC()
	}
	if n := len(ms); n > 0 && !m.At.After(ms[n-1].At) {
		m.At = ms[n-1].At.Add(time.Microsecond) // strictly in order, for "since"
	}
	if m.ID == "" {
		m.ID = strconv.FormatInt(m.At.UnixMicro(), 36)
	}
	ms = append(ms, m)
	if keepDays > 0 {
		cut := time.Now().AddDate(0, 0, -keepDays)
		ms = slices.DeleteFunc(ms, func(x ChatMessage) bool { return x.At.Before(cut) })
	}
	if len(ms) > maxChatMessages {
		ms = ms[len(ms)-maxChatMessages:]
	}
	return m, s.writeDoc(chatDoc(key), ms)
}

// chatState is what each person read last, and the status they typed.
type chatState struct {
	Read   map[string]map[string]time.Time `json:"read"`   // ext -> conversation -> last read
	Status map[string]string               `json:"status"` // ext -> "At lunch"
}

func (s *Store) loadChatState() chatState {
	var st chatState
	_ = s.readDoc("chat-state.json", &st)
	if st.Read == nil {
		st.Read = map[string]map[string]time.Time{}
	}
	if st.Status == nil {
		st.Status = map[string]string{}
	}
	return st
}

// MarkChatRead records that ext has read a conversation up to at.
func (s *Store) MarkChatRead(ext, key string, at time.Time) error {
	if !chatKeyRe.MatchString(key) {
		return ErrChatKey
	}
	chatMu.Lock()
	defer chatMu.Unlock()
	st := s.loadChatState()
	if st.Read[ext] == nil {
		st.Read[ext] = map[string]time.Time{}
	}
	if at.After(st.Read[ext][key]) {
		st.Read[ext][key] = at
	}
	return s.writeDoc("chat-state.json", st)
}

// ChatRead returns when ext last read each conversation.
func (s *Store) ChatRead(ext string) map[string]time.Time {
	chatMu.Lock()
	defer chatMu.Unlock()
	out := map[string]time.Time{}
	for k, v := range s.loadChatState().Read[ext] {
		out[k] = v
	}
	return out
}

// SetChatStatus sets the status a person shows colleagues ("At lunch"); empty clears it.
func (s *Store) SetChatStatus(ext, text string) error {
	chatMu.Lock()
	defer chatMu.Unlock()
	st := s.loadChatState()
	if text == "" {
		delete(st.Status, ext)
	} else {
		st.Status[ext] = text
	}
	return s.writeDoc("chat-state.json", st)
}

// ChatStatuses returns everyone's typed status.
func (s *Store) ChatStatuses() map[string]string {
	chatMu.Lock()
	defer chatMu.Unlock()
	return s.loadChatState().Status
}

// DirectKey is the conversation between two extensions.
func DirectKey(a, b string) string {
	if len(a) > len(b) || len(a) == len(b) && a > b {
		a, b = b, a
	}
	return "d-" + a + "-" + b
}
