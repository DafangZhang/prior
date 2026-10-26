package main

import (
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"prior/llm"
)

type settings struct {
	Version    int
	Connection struct {
		PGDSN     string
		OllamaURL string
	}
	Models struct {
		Embed string
		LLM   string
		// Protocol, Endpoint and APIKey are the language model's own: the
		// API it answers over, where it answers, and the key it wants. An
		// Ollama model answers over ProtocolOllama at the Ollama URL, with
		// no endpoint or key of its own.
		Protocol    string
		Endpoint    string
		APIKey      string
		Temperature float64
		NumCtx      int
		// MaxTokens bounds one answer's length; zero takes the protocol's own
		// default. A stage whose answer is cut is asked again over smaller
		// parts, so this is one call's room rather than a review's limit.
		MaxTokens  int
		TimeoutSec int
	}
	Appearance struct {
		Theme        string // system / light / dark
		Language     string // system / zh-Hans / en
		SidebarColor string // blue / violet / pink / orange / gold / green / cyan / gray
	}
	UI struct {
		SidebarHidden bool
		HiddenColumns []string
		Inspector     bool
	}
	Notifications struct {
		OnComplete bool
		OnError    bool
	}
}

const settingsVersion = 1

func defaultSettings() settings {
	var s settings
	s.Version = settingsVersion
	s.Connection.PGDSN = "postgres://postgres:postgres@127.0.0.1:5433/prior"
	s.Connection.OllamaURL = "http://localhost:11434"
	s.Models.Embed = "bge-m3:latest"
	s.Models.LLM = llm.DefaultModel(llm.ProtocolOllama)
	s.Models.Protocol = llm.ProtocolOllama
	s.Models.Temperature = 0.2
	s.Models.NumCtx = 8192
	s.Models.TimeoutSec = 300
	s.Appearance.Theme = "system"
	s.Appearance.Language = "system"
	s.Appearance.SidebarColor = "blue"
	s.UI.Inspector = true
	s.Notifications.OnComplete = true
	s.Notifications.OnError = true
	return s
}

func loadSettings(path string) settings {
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultSettings()
	}
	var s settings
	if err := json.Unmarshal(data, &s); err != nil {
		// backup corrupted file
		os.Rename(path, path+".bak")
		return defaultSettings()
	}
	if s.Version != settingsVersion {
		// normalize to current version
		def := defaultSettings()
		if s.Version < settingsVersion {
			s = def
		}
	}
	// The languages read "zh" and "en", as the catalogue is keyed; a file
	// from before that holds the locale's own tag.
	switch s.Appearance.Language {
	case "zh-Hans", "zh-Hant":
		s.Appearance.Language = "zh"
	}
	if s.Appearance.SidebarColor == "" {
		s.Appearance.SidebarColor = sidebarColors[0].name
	}
	return s
}

func (s settings) save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	// The file holds the provider's API key: create it readable by its owner
	// alone, and tighten a file written by an older build.
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func sameSettings(a, b settings) bool {
	return a.Version == b.Version &&
		a.Connection.PGDSN == b.Connection.PGDSN &&
		a.Connection.OllamaURL == b.Connection.OllamaURL &&
		a.Models.Embed == b.Models.Embed &&
		a.Models.LLM == b.Models.LLM &&
		a.Models.Temperature == b.Models.Temperature &&
		a.Models.NumCtx == b.Models.NumCtx &&
		a.Models.MaxTokens == b.Models.MaxTokens &&
		a.Models.TimeoutSec == b.Models.TimeoutSec &&
		a.Appearance.Theme == b.Appearance.Theme &&
		a.Appearance.Language == b.Appearance.Language &&
		a.UI.SidebarHidden == b.UI.SidebarHidden &&
		a.UI.Inspector == b.UI.Inspector &&
		a.Notifications.OnComplete == b.Notifications.OnComplete &&
		a.Notifications.OnError == b.Notifications.OnError
}

// pgFields is a PostgreSQL URL taken apart: the connection's own parts,
// which the settings page shows as fields, so that a password is typed
// into a field of its own rather than inside a connection string.
type pgFields struct {
	scheme   string
	host     string
	port     string
	database string
	user     string
	password string
	// query is the parameters the URL carried, as written: the page has no
	// fields for them, and rebuilding a DSN must not drop them.
	query string
}

// parsePGDSN reads a PostgreSQL URL into fields. A DSN in another shape,
// as pgx's key=value form, leaves every field empty: the page then builds
// a URL once one of them is typed into.
func parsePGDSN(dsn string) pgFields {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return pgFields{}
	}
	f := pgFields{
		scheme:   u.Scheme,
		host:     u.Hostname(),
		port:     u.Port(),
		database: strings.TrimPrefix(u.Path, "/"),
		query:    u.RawQuery,
	}
	if u.User != nil {
		f.user = u.User.Username()
		f.password, _ = u.User.Password()
	}
	return f
}

// dsn composes the fields back into a PostgreSQL URL, as the page's fields
// spell it. A host is what makes a connection string: without one there is
// nothing to connect to, and the fields make "".
func (f pgFields) dsn() string {
	if f.host == "" {
		return ""
	}
	scheme := f.scheme
	if scheme == "" {
		scheme = "postgres"
	}
	host := f.host
	if f.port != "" {
		host = net.JoinHostPort(f.host, f.port)
	}
	u := url.URL{Scheme: scheme, Host: host, RawQuery: f.query}
	if f.database != "" {
		u.Path = "/" + f.database
	}
	switch {
	case f.password != "":
		u.User = url.UserPassword(f.user, f.password)
	case f.user != "":
		// url.UserPassword would spell a password the DSN never had, as
		// "user:@host".
		u.User = url.User(f.user)
	}
	return u.String()
}
