package downloads

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/HolyArrs/holyarrs/app/backend/internal/core/database"
	domain "github.com/HolyArrs/holyarrs/app/backend/internal/core/model"
)

// fakeNoFeedQbit is a qBittorrent-compatible Downloader without a change
// feed, as Decypharr is: sync/maindata answers 404, torrents/info works.
type fakeNoFeedQbit struct {
	mu    sync.Mutex
	calls map[string]int
}

func (f *fakeNoFeedQbit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls[r.URL.Path]++
	f.mu.Unlock()
	switch strings.TrimPrefix(r.URL.Path, "/api/v2/") {
	case "sync/maindata":
		http.NotFound(w, r)
	case "torrents/info":
		_, _ = w.Write([]byte(`[{"hash":"` + ownHash + `","name":"Lab","category":"holyarrs","size":100,"progress":0.5,"state":"downloading","save_path":"/dl"}]`))
	case "app/version":
		_, _ = w.Write([]byte("v5.1.0"))
	case "app/webapiVersion":
		_, _ = w.Write([]byte("2.11.2"))
	default:
		_, _ = w.Write([]byte("[]"))
	}
}

func (f *fakeNoFeedQbit) count(p string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls["/api/v2/"+p]
}

func TestSync_QbitWithoutChangeFeedReadsTorrentsInfo(t *testing.T) {
	r := newRig(t)
	fake := &fakeNoFeedQbit{calls: map[string]int{}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	ctx := context.Background()
	if err := r.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE downloaders SET adapter = 'qbittorrent', url = ? WHERE id = ?", srv.URL, r.dlID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	r.m.Engine = func(d domain.Downloader) (Engine, error) { return NewQbittorrentKey(d.URL, "key", nil), nil }
	var d domain.Downloader
	if err := database.Get(ctx, r.db.Read(), "downloaders", r.dlID, &d); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if err := r.s.read(ctx, d); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
	if n := fake.count("sync/maindata"); n != 1 {
		t.Errorf("change feed asked %d times, want once", n)
	}
	if n := fake.count("torrents/info"); n < 3 {
		t.Errorf("torrents/info read %d times, want every read", n)
	}
	var state string
	var fails int
	if err := r.db.Read().QueryRowContext(ctx, "SELECT state, fail_count FROM downloaders WHERE id = ?", r.dlID).Scan(&state, &fails); err != nil {
		t.Fatal(err)
	}
	if state != "ok" || fails != 0 {
		t.Fatalf("Downloader state %q, fails %d: a missing change feed must not count as a failure", state, fails)
	}
	v := r.s.Store.View(r.dlID)
	// Fresh is what the Manager uses the snapshot on (no poller runs in this
	// test, so the view also says degraded).
	if !v.Fresh {
		t.Fatal("snapshot not fresh after a good read")
	}
	if _, ok := v.Items[ownHash]; !ok {
		t.Fatalf("own download missing from the snapshot: %v", v.Items)
	}
	if r.s.Store.Current(r.dlID).Fallback {
		t.Fatal("left in fallback")
	}
}

// Any other change-feed error still fails the read, as before.
func TestSync_QbitFeedErrorOtherThan404StillFails(t *testing.T) {
	s := &Sync{}
	if s.noteMainFeedMissing(1, 0, ErrAuth) || s.mainFeedMissing(1, 0) {
		t.Fatal("an auth error was taken for a missing feed")
	}
	if !s.noteMainFeedMissing(1, 0, ErrNotFound) || !s.mainFeedMissing(1, 0) || s.mainFeedMissing(1, 1) {
		t.Fatal("the 404 mark is wrong or outlives its generation")
	}
}
