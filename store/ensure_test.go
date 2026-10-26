package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestEnsureDatabaseOnlyOnMissing covers when the store creates a database:
// only when the server says the one it was asked for is not there. Any
// other failure — wrong password, no server — is the caller's to see, since
// creating anything would then be beside the point.
func TestEnsureDatabaseOnlyOnMissing(t *testing.T) {
	missing := &pgconn.PgError{Code: "3D000", Message: `database "prior" does not exist`}
	if created, err := ensureDatabase(context.Background(), "postgres://postgres:postgres@127.0.0.1:1/prior", missing); err == nil || created {
		// Port 1 has nothing listening: it must fail rather than pretend.
		if err == nil {
			t.Errorf("expected a connection error, got created=%v err=%v", created, err)
		}
	}
	auth := &pgconn.PgError{Code: "28P01", Message: "password authentication failed"}
	created, err := ensureDatabase(context.Background(), "postgres://postgres:wrong@127.0.0.1:5432/prior", auth)
	if created || !errors.Is(err, auth) {
		t.Errorf("an authentication failure must be passed through: created=%v err=%v", created, err)
	}
	plain := errors.New("connection refused")
	if _, err := ensureDatabase(context.Background(), "postgres://postgres:postgres@127.0.0.1:5432/prior", plain); !errors.Is(err, plain) {
		t.Errorf("a plain error must be passed through: %v", err)
	}
}
