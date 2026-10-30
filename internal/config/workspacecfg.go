package config

import (
	"net/mail"
	"regexp"
	"strings"
)

// Workspace is the organisation's Microsoft 365 or Google Workspace, read for its people, shared
// contacts and calendars. BitVoice only reads it.
type Workspace struct {
	// Provider is microsoft365, google, or empty for none.
	Provider string `json:"provider"`
	// Microsoft 365: an app registration in Entra ID (directory ID, application ID, a client
	// secret), with the application permissions User.Read.All, OrgContact.Read.All and
	// Calendars.ReadBasic, admin consent granted.
	TenantID     string `json:"tenantId"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	// Google Workspace: a service account's key file (JSON) with domain-wide delegation of the
	// scopes directory.readonly and calendar.freebusy, and an administrator it acts for.
	ServiceAccountKey string `json:"serviceAccountKey"`
	AdminEmail        string `json:"adminEmail"`
	// Contacts puts the organisation's people (with a phone number) and its shared contacts in
	// the company directory, kept in step every half hour.
	Contacts bool `json:"contacts"`
	// Calendars marks an extension away while its person (the extension's email) is busy, as a
	// calendar address does; the extension's calendar settings say what calls then do.
	Calendars bool `json:"calendars"`
}

var guidLike = regexp.MustCompile(`^[0-9a-fA-F-]{36}$|^[a-z0-9.-]+\.[a-z]{2,}$`)

// ValidateWorkspace checks the Microsoft 365 or Google Workspace settings alone (before trying them).
func (c *Config) ValidateWorkspace() Problems {
	var p Problems
	c.validateWorkspace(&p)
	return p
}

func (c *Config) validateWorkspace(p *Problems) {
	w := &c.System.Workspace
	w.Provider, w.TenantID, w.ClientID, w.AdminEmail = strings.TrimSpace(w.Provider), strings.TrimSpace(w.TenantID), strings.TrimSpace(w.ClientID), strings.TrimSpace(w.AdminEmail)
	switch w.Provider {
	case "":
		return
	case "microsoft365":
		if !guidLike.MatchString(w.TenantID) {
			p.add("system.workspace.tenantId", "give the directory (tenant) ID from Entra ID, App registrations, Overview (or the tenant's domain, contoso.onmicrosoft.com)")
		}
		if w.ClientID == "" {
			p.add("system.workspace.clientId", "give the application (client) ID of the app registration")
		}
		if strings.TrimSpace(w.ClientSecret) == "" {
			p.add("system.workspace.clientSecret", "give a client secret's value (Certificates & secrets, New client secret)")
		}
	case "google":
		if !strings.Contains(w.ServiceAccountKey, `"service_account"`) {
			p.add("system.workspace.serviceAccountKey", "paste the service account's key file (JSON), from Google Cloud, IAM, Service accounts, Keys")
		}
		if a, err := mail.ParseAddress(w.AdminEmail); err != nil || a.Address != w.AdminEmail {
			p.add("system.workspace.adminEmail", "give the email of a Workspace administrator the service account acts for")
		}
	default:
		p.add("system.workspace.provider", "choose Microsoft 365 or Google Workspace")
	}
}
