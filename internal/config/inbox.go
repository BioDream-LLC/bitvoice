package config

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	colourRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	digitsRe = regexp.MustCompile(`^[0-9]{5,30}$`)
)

// Inbox is the messaging inbox: WhatsApp, Facebook Messenger and the web chat on your site, answered
// by staff in the web interface beside the texts.
type Inbox struct {
	WebChat   WebChat   `json:"webChat"`
	WhatsApp  WhatsApp  `json:"whatsApp"`
	Messenger Messenger `json:"messenger"`
	// Greeting is sent automatically to the first message of a new conversation; empty sends none.
	Greeting string `json:"greeting"`
}

// WebChat is the chat box on your website (a script tag from BitVoice).
type WebChat struct {
	Enabled bool   `json:"enabled"`
	Title   string `json:"title"`   // at the top of the box ("Chat with us")
	Welcome string `json:"welcome"` // the first line visitors see
	Colour  string `json:"colour"`  // the button's colour, #RRGGBB
	// Origins are the sites the box may run on (https://www.example.com); empty lets any site.
	Origins []string `json:"origins"`
}

// WhatsApp is a WhatsApp Business number on Meta's Cloud API.
type WhatsApp struct {
	Enabled       bool   `json:"enabled"`
	PhoneNumberID string `json:"phoneNumberId"` // from WhatsApp Manager, not the phone number itself
	// BusinessAccountID is the WhatsApp business account's ID, to list its message templates.
	BusinessAccountID string `json:"businessAccountId,omitempty"`
	Token             string `json:"token"`       // a system user's permanent access token
	AppSecret         string `json:"appSecret"`   // the Meta app's secret, to check the webhooks
	VerifyToken       string `json:"verifyToken"` // what Meta sends to check the webhook address (made for you)
}

// Messenger is a Facebook Page's Messenger.
type Messenger struct {
	Enabled     bool   `json:"enabled"`
	PageID      string `json:"pageId"`
	PageToken   string `json:"pageToken"` // the page access token
	AppSecret   string `json:"appSecret"`
	VerifyToken string `json:"verifyToken"`
}

func (c *Config) validateInbox(p *Problems) {
	ib := &c.System.Inbox
	w := &ib.WebChat
	if w.Title == "" {
		w.Title = "Chat with us"
	}
	if len(w.Title) > 60 || len(w.Welcome) > 300 {
		p.add("system.inbox.webChat.title", "keep the title under 60 characters and the welcome under 300")
	}
	if w.Colour == "" {
		w.Colour = "#0ea5e9"
	}
	if !colourRe.MatchString(w.Colour) {
		p.add("system.inbox.webChat.colour", "a colour is # and six hex digits, such as #0ea5e9")
	}
	out := w.Origins[:0]
	for _, o := range w.Origins {
		o = strings.TrimRight(strings.TrimSpace(o), "/")
		if o == "" {
			continue
		}
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" {
			p.add("system.inbox.webChat.origins", "%q is not a site: use the scheme and name only, such as https://www.example.com", o)
		}
		out = append(out, o)
	}
	w.Origins = out
	wa := &ib.WhatsApp
	if wa.Enabled {
		if !digitsRe.MatchString(wa.PhoneNumberID) {
			p.add("system.inbox.whatsApp.phoneNumberId", "enter the phone number ID from WhatsApp Manager (digits)")
		}
		if wa.BusinessAccountID != "" && !digitsRe.MatchString(wa.BusinessAccountID) {
			p.add("system.inbox.whatsApp.businessAccountId", "enter the WhatsApp business account ID (digits), or leave it empty")
		}
		if wa.Token == "" {
			p.add("system.inbox.whatsApp.token", "enter the access token")
		}
		if wa.AppSecret == "" {
			p.add("system.inbox.whatsApp.appSecret", "enter the app secret: messages are checked with it")
		}
		if wa.VerifyToken == "" {
			wa.VerifyToken = NewID() + NewID()
		}
	}
	m := &ib.Messenger
	if m.Enabled {
		if !digitsRe.MatchString(m.PageID) {
			p.add("system.inbox.messenger.pageId", "enter the Facebook Page ID (digits)")
		}
		if m.PageToken == "" {
			p.add("system.inbox.messenger.pageToken", "enter the page access token")
		}
		if m.AppSecret == "" {
			p.add("system.inbox.messenger.appSecret", "enter the app secret: messages are checked with it")
		}
		if m.VerifyToken == "" {
			m.VerifyToken = NewID() + NewID()
		}
	}
	if len(ib.Greeting) > 500 {
		p.add("system.inbox.greeting", "keep the automatic greeting under 500 characters")
	}
}
