package connections

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/HolyArrs/holyarrs/app/backend/internal/core/database"
)

// The entries are one encrypted setting: no table, no migration. The
// settings table only takes a fixed list of groups; outside-service keys
// (Trakt and the like) live in "links", so these do too, under a key no
// upstream setting uses.
const (
	settingGroup = "links"
	settingKey   = "custom_connections_entries"
)

// Vault is the part of secrets.Vault the store uses.
type Vault interface {
	Setting(ctx context.Context, grp, key string) (string, error)
	SetSetting(ctx context.Context, grp, key, plain string, by database.Actor) error
}

func load(ctx context.Context, v Vault) ([]Entry, error) {
	raw, err := v.Setting(ctx, settingGroup, settingKey)
	if err != nil {
		return nil, fmt.Errorf("connections: read: %w", err)
	}
	if raw == "" {
		return nil, nil
	}
	var out []Entry
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("connections: stored list is not readable: %w", err)
	}
	return out, nil
}

func save(ctx context.Context, v Vault, es []Entry, by database.Actor) error {
	if es == nil {
		es = []Entry{}
	}
	b, err := json.Marshal(es)
	if err != nil {
		return err
	}
	if err := v.SetSetting(ctx, settingGroup, settingKey, string(b), by); err != nil {
		return fmt.Errorf("connections: save: %w", err)
	}
	return nil
}
