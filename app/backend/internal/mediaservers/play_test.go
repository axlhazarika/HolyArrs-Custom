package mediaservers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HolyArrs/holyarrs/app/backend/internal/core/database"
	domain "github.com/HolyArrs/holyarrs/app/backend/internal/core/model"
	"github.com/HolyArrs/holyarrs/app/backend/internal/mediafiles"
)

// fakeJelly is a Jellyfin that, like 12.4.0, ignores AnyProviderIdEquals:
// it filters by IncludeItemTypes, SearchTerm and ParentId only.
type fakeJelly struct {
	mu    sync.Mutex
	items []map[string]any
	calls []string
}

func (f *fakeJelly) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.Contains(r.Header.Get("Authorization"), `Token="`+tok+`"`) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.calls = append(f.calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	q := r.URL.Query()
	switch r.URL.Path {
	case "/System/Info/Public":
		_, _ = w.Write([]byte(`{"Id":"srv-1","Version":"12.4.0"}`))
	case "/Items":
		var out []map[string]any
		for _, it := range f.items {
			if typ := q.Get("IncludeItemTypes"); typ != "" && it["Type"] != typ {
				continue
			}
			if s := q.Get("SearchTerm"); s != "" && !strings.Contains(strings.ToLower(it["Name"].(string)), strings.ToLower(s)) {
				continue
			}
			if p := q.Get("ParentId"); p != "" && it["SeriesId"] != p {
				continue
			}
			out = append(out, it)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Items": out, "TotalRecordCount": len(out)})
	case "/Library/Media/Updated", "/Library/Refresh":
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeJelly) set(items ...map[string]any) { f.mu.Lock(); f.items = items; f.mu.Unlock() }

func (f *fakeJelly) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// playWorld is the Arrival world with a movie file in the Library, a show
// with one episode file, and a linked fake Jellyfin.
type playWorld struct {
	*world
	jelly           *fakeJelly
	srv             *httptest.Server
	l               *Linker
	moviePath       string
	show, episode   int64
	episodePath     string
	otherEpisodeTtl int64
}

func newPlayWorld(t *testing.T) *playWorld {
	t.Helper()
	playCache.Clear()
	serverIDs.Clear()
	w := &playWorld{world: newWorld(t), jelly: &fakeJelly{}}
	w.srv = httptest.NewServer(w.jelly)
	t.Cleanup(w.srv.Close)
	ctx := context.Background()
	err := w.s.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE titles SET year = 2016, source_ids = '{"tmdb":"329865","imdb":"tt2543164"}' WHERE id = ?`, w.title); err != nil {
			return err
		}
		var vid, lf int64
		if err := tx.QueryRow("SELECT id, library_folder_id FROM versions WHERE title_id = ?", w.title).Scan(&vid, &lf); err != nil {
			return err
		}
		it := &mediafiles.Item{TitleID: w.title, Kind: "movie"}
		if _, err := database.Insert(tx, "items", it, domain.System); err != nil {
			return err
		}
		w.moviePath = filepath.Join(w.lib, "Arrival (2016)", "Arrival (2016) 1080p.mkv")
		if err := addFile(tx, it.ID, vid, "Arrival (2016)/Arrival (2016) 1080p.mkv"); err != nil {
			return err
		}
		// A show in the same library folder, with one episode on disk.
		var pr int64
		_ = tx.QueryRow("SELECT id FROM pick_rules LIMIT 1").Scan(&pr)
		for i, name := range []string{"Severance", "Other Show"} {
			ti := &mediafiles.Title{Kind: "show", GateDriver: "none", Name: name, SortName: strings.ToLower(name), MetadataSource: "tvdb", NewPartsPolicy: "all",
				ReleaseGate: "released", WhyAdded: "by_hand", Status: "running", State: "active", SourceIDs: map[string]string{"tvdb": "371980"}, Aliases: []string{}, MusicTypes: []string{}, Artwork: []string{}}
			if _, err := database.Insert(tx, "titles", ti, domain.System); err != nil {
				return err
			}
			v := &mediafiles.Version{TitleID: ti.ID, Label: "HD", PickRulesID: pr, LibraryFolderID: lf, FolderName: name, IsMain: true}
			if _, err := database.Insert(tx, "versions", v, domain.System); err != nil {
				return err
			}
			s, e := 1, 2
			ep := &mediafiles.Item{TitleID: ti.ID, Kind: "episode", Numbers: mediafiles.Numbers{Season: &s, Episode: &e}}
			if _, err := database.Insert(tx, "items", ep, domain.System); err != nil {
				return err
			}
			if i == 0 {
				w.show, w.episode = ti.ID, ep.ID
				w.episodePath = filepath.Join(w.lib, "Severance", "Season 01", "Severance S01E02.mkv")
				if err := addFile(tx, ep.ID, v.ID, "Severance/Season 01/Severance S01E02.mkv"); err != nil {
					return err
				}
			} else {
				w.otherEpisodeTtl = ep.ID
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Add(ctx, w.s, w.v, Input{Name: "Jellyfin", Kind: "jellyfin", URL: w.srv.URL, Token: tok}, domain.System); err != nil {
		t.Fatal(err)
	}
	w.l = &Linker{Store: w.s, Vault: w.v, Delay: time.Hour}
	return w
}

func addFile(tx *sql.Tx, itemID, versionID int64, rel string) error {
	sl := &mediafiles.Slot{ItemID: itemID, VersionID: versionID, State: mediafiles.SlotInLibrary}
	if _, err := database.Insert(tx, "slots", sl, domain.System); err != nil {
		return err
	}
	f := &mediafiles.File{SlotIDs: []int64{sl.ID}, VersionID: versionID, RelativePath: rel, Size: 1, AddedAt: time.Now(),
		Languages: []string{}, MediaInfo: map[string]any{}, Origin: "imported", PlacedBy: "hardlink", Role: "main", State: "present"}
	if _, err := database.Insert(tx, "files", f, domain.System); err != nil {
		return err
	}
	_, err := tx.Exec("UPDATE slots SET file_id = ? WHERE id = ?", f.ID, sl.ID)
	return err
}

func movie(id, name, path string, year int, ids map[string]string) map[string]any {
	return map[string]any{"Id": id, "Type": "Movie", "Name": name, "Path": path, "ProductionYear": year, "ProviderIds": ids}
}

func TestPlayFindsTheMovieByItsFile(t *testing.T) {
	w := newPlayWorld(t)
	// Two items share the ids; only one holds this file.
	w.jelly.set(movie("other", "Arrival", "/elsewhere/Arrival.mkv", 2016, map[string]string{"Tmdb": "329865"}),
		movie("right", "Arrival", w.moviePath, 2016, map[string]string{"Imdb": "tt2543164"}))
	ans, err := w.l.Play(context.Background(), w.title, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	want := w.srv.URL + "/web/#/details?id=right&serverId=srv-1"
	if ans.URL != want || ans.Why != "" || ans.Server != "Jellyfin" {
		t.Fatalf("answer %+v, want %s", ans, want)
	}
	if b, _ := json.Marshal(ans); strings.Contains(string(b), tok) {
		t.Fatalf("the key is in the answer: %s", b)
	}
	// Cached: a second Play asks Jellyfin nothing more.
	before := w.jelly.count("GET /Items")
	if _, err := w.l.Play(context.Background(), w.title, 0, false); err != nil || w.jelly.count("GET /Items") != before {
		t.Fatalf("not cached: %v", err)
	}
}

func TestPlayFallsBackToProviderIDs(t *testing.T) {
	w := newPlayWorld(t)
	// Jellyfin sees the file under another path (no path map set) and only
	// knows the imdb id; another film with the name is a year off by far.
	w.jelly.set(movie("old", "Arrival", "/other/Arrival 1996.mkv", 1996, map[string]string{"Imdb": "tt0115571"}),
		movie("ids", "Arrival", "/mnt/media/Arrival (2016).mkv", 2016, map[string]string{"Imdb": "tt2543164"}))
	ans, err := w.l.Play(context.Background(), w.title, 0, false)
	if err != nil || !strings.Contains(ans.URL, "id=ids&") {
		t.Fatalf("answer %+v %v", ans, err)
	}
}

func TestPlayNotInJellyfinYet(t *testing.T) {
	w := newPlayWorld(t)
	w.jelly.set(movie("x", "Another Film", "/x.mkv", 2001, map[string]string{"Imdb": "tt1"}))
	ans, err := w.l.Play(context.Background(), w.title, 0, true)
	if err != nil || ans.URL != "" || ans.Why != whyNotAdded || !ans.CanScan {
		t.Fatalf("answer %+v %v", ans, err)
	}
	if ans, _ := w.l.Play(context.Background(), w.title, 0, false); ans.CanScan {
		t.Fatal("a member was offered the scan")
	}
	// Asking Jellyfin to look tells it the movie folder and forgets the miss.
	if _, err := w.l.PlayScan(context.Background(), w.title, 0); err != nil {
		t.Fatal(err)
	}
	if w.jelly.count("POST /Library/Media/Updated") != 1 {
		t.Fatalf("calls %v", w.jelly.calls)
	}
	w.jelly.set(movie("now", "Arrival", w.moviePath, 2016, nil))
	if ans, _ := w.l.Play(context.Background(), w.title, 0, false); !strings.Contains(ans.URL, "id=now&") {
		t.Fatalf("after the scan %+v", ans)
	}
}

func TestPlayFindsTheEpisode(t *testing.T) {
	w := newPlayWorld(t)
	w.jelly.set(
		map[string]any{"Id": "ser", "Type": "Series", "Name": "Severance", "Path": filepath.Join(w.lib, "Severance"), "ProviderIds": map[string]string{"Tvdb": "371980"}},
		map[string]any{"Id": "e1", "Type": "Episode", "Name": "Pilot", "SeriesId": "ser", "ParentIndexNumber": 1, "IndexNumber": 1, "Path": "/x/1.mkv"},
		map[string]any{"Id": "e2", "Type": "Episode", "Name": "Half Loop", "SeriesId": "ser", "ParentIndexNumber": 1, "IndexNumber": 2, "Path": w.episodePath},
	)
	ans, err := w.l.Play(context.Background(), w.show, w.episode, false)
	if err != nil || !strings.Contains(ans.URL, "id=e2&") {
		t.Fatalf("answer %+v %v", ans, err)
	}
	// An episode of another Title is not this Title's.
	if _, err := w.l.Play(context.Background(), w.show, w.otherEpisodeTtl, false); err != mediafiles.ErrNoPlayItem {
		t.Fatalf("another title's episode: %v", err)
	}
	// A show without an item, and a movie with one, are refused too.
	if _, err := w.l.Play(context.Background(), w.show, 0, false); err != mediafiles.ErrNoPlayItem {
		t.Fatalf("show without item: %v", err)
	}
	if _, err := w.l.Play(context.Background(), w.title, w.episode, false); err != mediafiles.ErrNoPlayItem {
		t.Fatalf("movie with item: %v", err)
	}
}

func TestPlayUsesTheViewerAddress(t *testing.T) {
	w := newPlayWorld(t)
	w.jelly.set(movie("right", "Arrival", w.moviePath, 2016, nil))
	ctx := context.Background()
	ms, _ := listServers(ctx, w.s.Read())
	for _, bad := range []string{"ftp://x", "http://", "http://h/web", "http://u:p@h", "http://h?x=1", "jellyfin.local"} {
		if _, err := w.l.SetPlayURL(ctx, ms[0].ID, bad, domain.System); err != ErrPlayURL {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if v, err := w.l.SetPlayURL(ctx, ms[0].ID, " http://192.168.1.20:8096/ ", domain.System); err != nil || v != "http://192.168.1.20:8096" {
		t.Fatalf("set %q %v", v, err)
	}
	ans, _ := w.l.Play(ctx, w.title, 0, false)
	if ans.URL != "http://192.168.1.20:8096/web/#/details?id=right&serverId=srv-1" {
		t.Fatalf("url %q", ans.URL)
	}
	if _, err := w.l.SetPlayURL(ctx, ms[0].ID, "", domain.System); err != nil {
		t.Fatal(err)
	}
	if ans, _ := w.l.Play(ctx, w.title, 0, false); !strings.HasPrefix(ans.URL, w.srv.URL+"/web/") {
		t.Fatalf("url %q", ans.URL)
	}
	if _, err := w.l.PlayURL(ctx, 999); err == nil {
		t.Fatal("unknown server")
	}
}

func TestPlayWithoutJellyfin(t *testing.T) {
	w := newPlayWorld(t)
	ctx := context.Background()
	ms, _ := listServers(ctx, w.s.Read())
	if err := w.s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE media_servers SET kind = 'plex' WHERE id = ?", ms[0].ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if ans, _ := w.l.Play(ctx, w.title, 0, true); ans.Why != whyOnlyJelly || ans.URL != "" {
		t.Fatalf("plex only %+v", ans)
	}
	if err := w.s.Write(ctx, func(tx *sql.Tx) error { _, err := tx.Exec("DELETE FROM media_servers"); return err }); err != nil {
		t.Fatal(err)
	}
	if ans, _ := w.l.Play(ctx, w.title, 0, true); ans.Why != whyNoServer {
		t.Fatalf("none %+v", ans)
	}
}

func TestPlayJellyfinDown(t *testing.T) {
	w := newPlayWorld(t)
	w.srv.Close()
	ans, err := w.l.Play(context.Background(), w.title, 0, true)
	if err != nil || ans.Why != whyNoAnswer || ans.URL != "" {
		t.Fatalf("answer %+v %v", ans, err)
	}
}
