package mediaservers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	domain "github.com/HolyArrs/holyarrs/app/backend/internal/core/model"
	"github.com/HolyArrs/holyarrs/app/backend/internal/mediafiles"
)

// Play links a movie or episode in the Library to the same item in
// Jellyfin's web app, where it plays with Jellyfin's own resume, tracks and
// history. HolyArrs hands the browser only the link: the server's key never
// leaves the server.
//
// The item is found by its file: the Jellyfin item whose path is the
// file's path (through the server's path map). When no path matches, a
// movie falls back to the Title's tmdb or imdb id and year, an episode to
// its show's ids and its season and episode numbers. Jellyfin ignores the
// AnyProviderIdEquals filter, so items are listed by name, then in pages,
// and compared here.

// PlayAnswer is what a Play button shows: a link, or why there is none.
// State is ready, no_file, no_server, not_added or no_answer.
type PlayAnswer struct {
	URL     string `json:"url"`
	State   string `json:"state"`
	Server  string `json:"server,omitempty"`
	Why     string `json:"why,omitempty"`
	CanScan bool   `json:"can_scan,omitempty"`
}

// Reasons a Play button has no link.
const (
	whyNoFile     = "this has no file in the Library yet"
	whyNoServer   = "link Jellyfin under Settings > Media servers to play from here"
	whyOnlyJelly  = "Play works with Jellyfin only for now"
	whyNotAdded   = "Jellyfin hasn't added this file yet"
	whyNoAnswer   = "Jellyfin did not answer"
	playPageLimit = 200
	playMaxPages  = 25 // 5000 items at most are compared
)

// player finds an item on one kind of server and links to it. Jellyfin is
// the only kind for now; Emby and Plex would add their own.
type player interface {
	find(ctx context.Context, l *Linker, m domain.MediaServer, t mediafiles.PlayTarget) (string, error)
	link(ctx context.Context, l *Linker, m domain.MediaServer, base, itemID string) (string, error)
}

var players = map[string]player{"jellyfin": jellyfinPlayer{}}

// playCache keeps found item ids for a while. The key holds the file's
// path, so a renamed file is looked up again. A miss is kept briefly so a
// busy page does not ask again and again.
type playEntry struct {
	id    string
	until time.Time
}

type playKey struct {
	l    *Linker
	m    int64
	path string
}

var (
	playCache sync.Map // playKey -> playEntry
	serverIDs sync.Map // playKey{l, m, ""} -> playEntry (Jellyfin's server id)
)

const (
	playHit  = 6 * time.Hour
	playMiss = 2 * time.Minute
)

// Play returns the link for a Title's movie (itemID 0) or one of its
// episodes. canScan says whether the caller may ask the server to look for
// a missing file.
func (l *Linker) Play(ctx context.Context, titleID, itemID int64, canScan bool) (PlayAnswer, error) {
	t, err := mediafiles.PlayFiles(ctx, l.Store.Read(), titleID, itemID)
	if err != nil {
		return PlayAnswer{}, err
	}
	if len(t.Paths) == 0 {
		return PlayAnswer{State: "no_file", Why: whyNoFile}, nil
	}
	m, p, why, err := l.playServer(ctx)
	if err != nil || why != "" {
		return PlayAnswer{State: "no_server", Why: why}, err
	}
	key := playKey{l, m.ID, t.Kind + "|" + t.Paths[0]}
	id := ""
	if e, ok := playCache.Load(key); ok && time.Now().Before(e.(playEntry).until) {
		id = e.(playEntry).id
	} else {
		id, err = p.find(ctx, l, m, t)
		if err != nil {
			return PlayAnswer{State: "no_answer", Server: m.Name, Why: whyNoAnswer}, nil
		}
		ttl := playHit
		if id == "" {
			ttl = playMiss
		}
		playCache.Store(key, playEntry{id: id, until: time.Now().Add(ttl)})
	}
	if id == "" {
		return PlayAnswer{State: "not_added", Server: m.Name, Why: whyNotAdded, CanScan: canScan}, nil
	}
	base, err := l.PlayURL(ctx, m.ID)
	if err != nil {
		return PlayAnswer{}, err
	}
	if base == "" {
		base = m.URL
	}
	link, err := p.link(ctx, l, m, base, id)
	if err != nil {
		return PlayAnswer{State: "no_answer", Server: m.Name, Why: whyNoAnswer}, nil
	}
	return PlayAnswer{URL: link, State: "ready", Server: m.Name}, nil
}

// PlayScan asks the server to look at the item's files now and forgets
// what was cached about them.
func (l *Linker) PlayScan(ctx context.Context, titleID, itemID int64) (PlayAnswer, error) {
	t, err := mediafiles.PlayFiles(ctx, l.Store.Read(), titleID, itemID)
	if err != nil {
		return PlayAnswer{}, err
	}
	if len(t.Paths) == 0 {
		return PlayAnswer{State: "no_file", Why: whyNoFile}, nil
	}
	m, _, why, err := l.playServer(ctx)
	if err != nil || why != "" {
		return PlayAnswer{State: "no_server", Why: why}, err
	}
	kind := "movie"
	if t.Kind == "episode" {
		kind = "show"
	}
	var us []Update
	for _, p := range t.Paths {
		us = append(us, Update{Path: MapPath(m, filepath.Dir(p)), Change: Created, Kind: kind})
		playCache.Delete(playKey{l, m.ID, t.Kind + "|" + p})
	}
	if err := l.Tell(ctx, m, us); err != nil {
		return PlayAnswer{State: "no_answer", Server: m.Name, Why: whyNoAnswer}, nil
	}
	return PlayAnswer{State: "not_added", Server: m.Name, Why: whyNotAdded}, nil
}

// playServer is the first enabled Jellyfin.
func (l *Linker) playServer(ctx context.Context) (domain.MediaServer, player, string, error) {
	ms, err := enabledServers(ctx, l.Store.Read())
	if err != nil {
		return domain.MediaServer{}, nil, "", err
	}
	for _, m := range ms {
		if p, ok := players[m.Kind]; ok {
			return m, p, "", nil
		}
	}
	if len(ms) > 0 {
		return domain.MediaServer{}, nil, whyOnlyJelly, nil
	}
	return domain.MediaServer{}, nil, whyNoServer, nil
}

type jellyfinPlayer struct{}

// jellyPlayItem is the part of a Jellyfin item Play compares.
type jellyPlayItem struct {
	ID                string            `json:"Id"`
	Type              string            `json:"Type"`
	Path              string            `json:"Path"`
	ProductionYear    int               `json:"ProductionYear"`
	IndexNumber       *int              `json:"IndexNumber"`
	ParentIndexNumber *int              `json:"ParentIndexNumber"`
	ProviderIDs       map[string]string `json:"ProviderIds"`
}

func (jellyfinPlayer) find(ctx context.Context, l *Linker, m domain.MediaServer, t mediafiles.PlayTarget) (string, error) {
	paths := map[string]bool{}
	for _, p := range t.Paths {
		paths[samePath(MapPath(m, p))] = true
	}
	if t.Kind == "movie" {
		return jellyFind(ctx, l, m, "IncludeItemTypes=Movie", t.Name, func(it jellyPlayItem) int {
			switch {
			case paths[samePath(it.Path)]:
				return 2
			case sameIDs(it.ProviderIDs, t.IDs, "tmdb", "imdb") && (t.Year == 0 || it.ProductionYear == 0 || absInt(it.ProductionYear-t.Year) <= 1):
				return 1
			}
			return 0
		})
	}
	// An episode: its show first, then the episode under it.
	show, err := jellyFind(ctx, l, m, "IncludeItemTypes=Series", t.Name, func(it jellyPlayItem) int {
		if sameIDs(it.ProviderIDs, t.IDs, "tvdb", "tmdb", "imdb") {
			return 2
		}
		for p := range paths {
			if it.Path != "" && strings.HasPrefix(p, samePath(it.Path)+"/") {
				return 1
			}
		}
		return 0
	})
	if err != nil || show == "" {
		return "", err
	}
	return jellyFind(ctx, l, m, "IncludeItemTypes=Episode&ParentId="+url.QueryEscape(show), "", func(it jellyPlayItem) int {
		switch {
		case paths[samePath(it.Path)]:
			return 2
		case t.Season > 0 && t.Episode > 0 && it.ParentIndexNumber != nil && it.IndexNumber != nil &&
			*it.ParentIndexNumber == t.Season && *it.IndexNumber == t.Episode:
			return 1
		}
		return 0
	})
}

// jellyFind lists items (by name first when given, then every item of the
// kind in pages) and returns the id that scores best; a score of 2 ends the
// search.
func jellyFind(ctx context.Context, l *Linker, m domain.MediaServer, filter, name string, score func(jellyPlayItem) int) (string, error) {
	best, bestScore := "", 0
	look := func(q string) (int, error) {
		b, err := l.do(ctx, m, http.MethodGet, "/Items?Recursive=true&Fields=Path,ProviderIds&"+filter+q, nil)
		if err != nil {
			return 0, err
		}
		var page struct {
			Items []jellyPlayItem `json:"Items"`
		}
		if err := json.Unmarshal(b, &page); err != nil {
			return 0, errors.New("the server's answer could not be read")
		}
		for _, it := range page.Items {
			if s := score(it); s > bestScore && it.ID != "" {
				best, bestScore = it.ID, s
			}
		}
		return len(page.Items), nil
	}
	if name != "" {
		if _, err := look("&Limit=50&SearchTerm=" + url.QueryEscape(name)); err != nil {
			return "", err
		}
		if bestScore == 2 {
			return best, nil
		}
	}
	for i := range playMaxPages {
		n, err := look("&Limit=" + strconv.Itoa(playPageLimit) + "&StartIndex=" + strconv.Itoa(i*playPageLimit))
		if err != nil {
			return "", err
		}
		if bestScore == 2 || n < playPageLimit {
			break
		}
	}
	return best, nil
}

// link builds the web app address of the item. Jellyfin's web app needs
// its server id, which its public system info gives without a key.
func (jellyfinPlayer) link(ctx context.Context, l *Linker, m domain.MediaServer, base, itemID string) (string, error) {
	sk := playKey{l, m.ID, ""}
	sid := ""
	if e, ok := serverIDs.Load(sk); ok && time.Now().Before(e.(playEntry).until) {
		sid = e.(playEntry).id
	} else {
		b, err := l.do(ctx, m, http.MethodGet, "/System/Info/Public", nil)
		if err != nil {
			return "", err
		}
		var info struct {
			ID string `json:"Id"`
		}
		if err := json.Unmarshal(b, &info); err != nil || info.ID == "" {
			return "", errors.New("the server gave no id")
		}
		sid = info.ID
		serverIDs.Store(sk, playEntry{id: sid, until: time.Now().Add(playHit)})
	}
	return strings.TrimRight(base, "/") + "/web/#/details?id=" + url.QueryEscape(itemID) + "&serverId=" + url.QueryEscape(sid), nil
}

// samePath compares paths the way a server may report them: slashes
// either way, no trailing slash.
func samePath(p string) string {
	return strings.TrimRight(strings.ReplaceAll(p, `\`, "/"), "/")
}

// sameIDs says whether a server item and the Title share one of the named
// ids (the server writes the keys capitalised: Tmdb, Imdb, Tvdb).
func sameIDs(server, ours map[string]string, keys ...string) bool {
	for k, v := range server {
		for _, want := range keys {
			if strings.EqualFold(k, want) && v != "" && strings.EqualFold(v, ours[want]) {
				return true
			}
		}
	}
	return false
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
