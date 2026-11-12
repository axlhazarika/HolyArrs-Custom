package connections

import (
	"context"
	"strings"
	"testing"

	"github.com/HolyArrs/holyarrs/app/backend/internal/core/database"
	"github.com/HolyArrs/holyarrs/app/backend/internal/core/secrets"
)

// TestStoreOnARealDatabase runs the store against a migrated database and
// the real vault, so the settings table's own checks apply.
func TestStoreOnARealDatabase(t *testing.T) {
	ctx := context.Background()
	s, err := database.Open(t.TempDir(), database.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	v, err := secrets.Open(ctx, s, make([]byte, secrets.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	if es, err := load(ctx, v); err != nil || len(es) != 0 {
		t.Fatalf("empty load: %v %v", es, err)
	}
	want := []Entry{{ID: "a1", Kind: KindDebrid, Provider: RealDebrid, Name: "holyarrs-realdebrid", Secret: "rd-secret-key", Enabled: true, State: StateOK}}
	if err := save(ctx, v, want, by); err != nil {
		t.Fatal(err)
	}
	want[0].Secret = "rd-secret-key-2"
	if err := save(ctx, v, want, by); err != nil {
		t.Fatal(err)
	}
	got, err := load(ctx, v)
	if err != nil || len(got) != 1 || got[0].Secret != "rd-secret-key-2" || got[0].Name != "holyarrs-realdebrid" {
		t.Fatalf("load = %+v, %v", got, err)
	}
	// The settings row holds only a reference; the key is sealed in secrets.
	var raw string
	if err := s.Read().QueryRowContext(ctx, "SELECT value FROM settings WHERE grp = ? AND key = ?", settingGroup, settingKey).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "secret:") || strings.Contains(raw, "rd-secret") {
		t.Fatalf("settings row = %q", raw)
	}

	// The whole service saves through the same store.
	svc := &Service{Vault: v, Decypharr: &Decypharr{}}
	if _, err := svc.Add(ctx, Input{Kind: KindDebrid, Provider: AllDebrid, Name: "holyarrs-alldebrid", Secret: "ad-key"}, by); err != nil {
		t.Fatal(err)
	}
	if got, _ := load(ctx, v); len(got) != 2 {
		t.Fatalf("after Add: %d entries", len(got))
	}
}
