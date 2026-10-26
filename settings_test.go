package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestParsePGDSN covers the Environment page's connection fields: what a
// DSN says they hold, and what they spell back.
func TestParsePGDSN(t *testing.T) {
	f := parsePGDSN("postgres://postgres:secret@127.0.0.1:5433/prior")
	if f.host != "127.0.0.1" || f.port != "5433" || f.database != "prior" ||
		f.user != "postgres" || f.password != "secret" {
		t.Fatalf("the fields read %+v", f)
	}
	// A DSN the app itself writes is what the fields spell: opening the
	// page must not rewrite what it shows.
	f = parsePGDSN("postgres://prior:p%40ss%2Fw%3Ard@db.example.com/prior?sslmode=disable")
	if f.port != "" || f.password != "p@ss/w:rd" {
		t.Fatalf("the fields read %+v", f)
	}
	if got := f.dsn(); got != "postgres://prior:p%40ss%2Fw%3Ard@db.example.com/prior?sslmode=disable" {
		t.Fatalf("the fields spell %q", got)
	}
	for _, dsn := range []string{
		"postgres://postgres:postgres@127.0.0.1:5433/prior",
		"postgresql://postgres@[::1]:5432/prior",
		"postgres://u:%E5%AF%86%E7%A0%81@127.0.0.1:5433/prior?pool_max_conns=4",
	} {
		if got := parsePGDSN(dsn).dsn(); got != dsn {
			t.Errorf("%q comes back as %q", dsn, got)
		}
	}
	// A DSN in another shape, and fields without a host, spell nothing:
	// neither is a connection string the page can take apart.
	if got := parsePGDSN("host=127.0.0.1 user=postgres").dsn(); got != "" {
		t.Errorf("a key=value DSN comes back as %q", got)
	}
	if got := (pgFields{user: "postgres", database: "prior"}).dsn(); got != "" {
		t.Errorf("fields without a host spell %q", got)
	}
}

// TestSettingsFileIsOwnerOnly covers the API key the settings file holds:
// on a unix-like system the file is readable by its owner alone, including
// a file an older build wrote with looser permissions.
func TestSettingsFileIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := defaultSettings().save(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("settings file mode = %o, want 600", got)
	}
}
