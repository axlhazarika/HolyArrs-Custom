package mediaservers

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/HolyArrs/holyarrs/app/backend/internal/core/database"
	domain "github.com/HolyArrs/holyarrs/app/backend/internal/core/model"
)

// The address viewers' browsers use for a server's web app is a plain
// setting per server, so the API address HolyArrs calls can stay a local
// one. Empty means the API address.
const playURLGroup = "links"

func playURLKey(id int64) string { return "custom_play_url_" + strconv.FormatInt(id, 10) }

// ErrPlayURL is a viewer address that is not a plain http or https address.
var ErrPlayURL = errors.New("give an http:// or https:// address, such as http://192.168.1.20:8096, with no path after the port")

// PlayURL returns the viewer address of server id ("" for the API address).
func (l *Linker) PlayURL(ctx context.Context, id int64) (string, error) {
	if _, err := getServer(ctx, l.Store.Read(), id); err != nil {
		return "", err
	}
	return database.Setting(ctx, l.Store.Read(), playURLGroup, playURLKey(id))
}

// SetPlayURL stores the viewer address of server id; "" goes back to the
// API address.
func (l *Linker) SetPlayURL(ctx context.Context, id int64, raw string, by domain.Actor) (string, error) {
	v, err := cleanPlayURL(raw)
	if err != nil {
		return "", err
	}
	if _, err := getServer(ctx, l.Store.Read(), id); err != nil {
		return "", err
	}
	err = l.Store.Write(ctx, func(tx *sql.Tx) error {
		return database.PutSetting(tx, playURLGroup, playURLKey(id), v, by, time.Now())
	})
	return v, err
}

func cleanPlayURL(raw string) (string, error) {
	s := strings.TrimRight(strings.TrimSpace(raw), "/")
	if s == "" {
		return "", nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", ErrPlayURL
	}
	return u.Scheme + "://" + u.Host, nil
}
