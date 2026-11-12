package connections

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/HolyArrs/holyarrs/app/backend/internal/core/secrets"
)

// Kinds of entry.
const (
	KindUsenet = "usenet"
	KindDebrid = "debrid"
)

// Debrid providers this module manages.
const (
	RealDebrid = "realdebrid"
	AllDebrid  = "alldebrid"
)

// States of an entry.
const (
	StatePending = "pending" // saved here, not yet in the target app
	StateOK      = "ok"
	StateFailed  = "failed"
	StateOff     = "disabled"
)

// sabPrefix starts the SABnzbd keyword of every server this module manages,
// so servers made in SABnzbd itself are never touched.
const sabPrefix = "holyarrs-"

// Entry is one Usenet server or debrid account. Secret is the server
// password or the debrid API key; it is never sent to the browser.
type Entry struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Provider    string    `json:"provider,omitempty"`
	Name        string    `json:"name"`
	Host        string    `json:"host,omitempty"`
	Port        int       `json:"port,omitempty"`
	SSL         bool      `json:"ssl,omitempty"`
	Username    string    `json:"username,omitempty"`
	Secret      string    `json:"secret,omitempty"`
	Connections int       `json:"connections,omitempty"`
	Priority    int       `json:"priority,omitempty"`
	Enabled     bool      `json:"enabled"`
	State       string    `json:"state"`
	LastPush    time.Time `json:"lastPush,omitzero"`
	LastError   string    `json:"lastError,omitempty"`
	// PushedName is the Decypharr name the account was last written under,
	// so a rename replaces the old entry instead of leaving it behind.
	PushedName string `json:"pushedName,omitempty"`
	PushedKey  string `json:"pushedKey,omitempty"`
}

// View is an Entry as the browser sees it.
type View struct {
	Entry
	HasSecret bool `json:"hasSecret"`
}

// ViewOf masks the secret.
func ViewOf(e Entry) View {
	v := View{Entry: e, HasSecret: e.Secret != ""}
	v.Secret = secrets.Mask(e.Secret)
	v.PushedName, v.PushedKey = "", ""
	return v
}

// Input is what the browser sends to add or change an entry. A Secret equal
// to secrets.Masked or empty on a change keeps the stored one.
type Input struct {
	Kind        string `json:"kind"`
	Provider    string `json:"provider"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	SSL         bool   `json:"ssl"`
	Username    string `json:"username"`
	Secret      string `json:"secret"`
	Connections int    `json:"connections"`
	Priority    int    `json:"priority"`
	Enabled     *bool  `json:"enabled"`
}

var (
	reName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,62}$`)
	reHost = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
)

// apply checks in and copies it onto e. isNew entries need a secret.
func apply(in Input, e *Entry, isNew bool) error {
	if isNew {
		e.Kind = in.Kind
	} else if in.Kind != "" && in.Kind != e.Kind {
		return errors.New("the kind of an entry cannot change; remove it and add a new one")
	}
	name := strings.TrimSpace(in.Name)
	if !reName.MatchString(name) {
		return errors.New("name: use 1 to 63 letters, digits, spaces, dots, dashes or underscores")
	}
	e.Name = name
	switch e.Kind {
	case KindUsenet:
		host := strings.TrimSpace(in.Host)
		if !reHost.MatchString(host) {
			return errors.New("host: give the server's host name, without http:// or a port")
		}
		if in.Port < 1 || in.Port > 65535 {
			return errors.New("port: 1 to 65535 (563 for SSL, 119 without)")
		}
		if in.Connections < 1 || in.Connections > 100 {
			return errors.New("connections: 1 to 100")
		}
		if in.Priority < 0 || in.Priority > 99 {
			return errors.New("priority: 0 (first) to 99")
		}
		if strings.ContainsAny(in.Username, "\r\n") {
			return errors.New("username: one line only")
		}
		e.Host, e.Port, e.SSL, e.Connections, e.Priority = host, in.Port, in.SSL, in.Connections, in.Priority
		e.Username = strings.TrimSpace(in.Username)
		e.Provider = ""
	case KindDebrid:
		if !isNew && in.Provider != "" && in.Provider != e.Provider {
			return errors.New("the provider of an account cannot change; remove it and add a new one")
		}
		if isNew {
			if in.Provider != RealDebrid && in.Provider != AllDebrid {
				return errors.New("provider: realdebrid or alldebrid")
			}
			e.Provider = in.Provider
		}
		e.Host, e.Port, e.SSL, e.Username, e.Connections, e.Priority = "", 0, false, "", 0, 0
	default:
		return errors.New("kind: usenet or debrid")
	}
	sec := strings.TrimSpace(in.Secret)
	if sec != "" && sec != secrets.Masked {
		if strings.ContainsAny(sec, "\r\n") {
			return errors.New("the password or key must be one line")
		}
		e.Secret = sec
	}
	if e.Secret == "" && (e.Kind == KindDebrid || isNew && e.Username != "") {
		if e.Kind == KindDebrid {
			return errors.New("give the account's API key")
		}
		return errors.New("give the server's password")
	}
	if in.Enabled != nil {
		e.Enabled = *in.Enabled
	} else if isNew {
		e.Enabled = true
	}
	return nil
}

func newID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("connections: random id: %v", err))
	}
	return hex.EncodeToString(b)
}

// sabKeyword is the SABnzbd server name of e.
func sabKeyword(e Entry) string { return sabPrefix + e.ID }
