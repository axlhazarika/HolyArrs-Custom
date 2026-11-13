package mediafiles

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/HolyArrs/holyarrs/app/backend/internal/core/database"
)

// PlayTarget is what a media server needs to find the movie or episode a
// Play button stands for: the Title's name, year and ids, the episode's
// numbers, and the full paths of the item's files in the Library.
type PlayTarget struct {
	Kind    string // movie or episode
	Name    string // the Title's name (a show's name for an episode)
	Year    int
	IDs     map[string]string // the Title's source ids: tmdb, tvdb, imdb
	Season  int
	Episode int
	Paths   []string // present files, main Version first
}

// ErrNoPlayItem means the item is not a movie or episode of that Title.
var ErrNoPlayItem = errors.New("no such movie or episode")

// PlayQuerier reads rows and single rows: a pool or a transaction.
type PlayQuerier interface {
	database.Querier
	database.Lister
}

// PlayFiles reads the Play target of a Title's movie (itemID 0) or of one
// of its episodes. An item of another Title is refused, so the Title's own
// Kid check covers the item too.
func PlayFiles(ctx context.Context, q PlayQuerier, titleID, itemID int64) (PlayTarget, error) {
	var t PlayTarget
	var kind, ids string
	var year sql.NullInt64
	err := q.QueryRowContext(ctx, "SELECT kind, name, year, source_ids FROM titles WHERE id = ? AND state != 'removed'", titleID).Scan(&kind, &t.Name, &year, &ids)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNoPlayItem
	}
	if err != nil {
		return t, err
	}
	t.Year = int(year.Int64)
	_ = json.Unmarshal([]byte(ids), &t.IDs)
	var nums string
	switch {
	case kind == "movie" && itemID == 0:
		t.Kind = "movie"
		err = q.QueryRowContext(ctx, "SELECT id FROM items WHERE title_id = ? AND kind = 'movie' ORDER BY id LIMIT 1", titleID).Scan(&itemID)
	case kind == "show" && itemID != 0:
		t.Kind = "episode"
		err = q.QueryRowContext(ctx, "SELECT numbers FROM items WHERE id = ? AND title_id = ? AND kind = 'episode'", itemID, titleID).Scan(&nums)
	default:
		return t, ErrNoPlayItem
	}
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNoPlayItem
	}
	if err != nil {
		return t, err
	}
	if nums != "" {
		var n Numbers
		_ = json.Unmarshal([]byte(nums), &n)
		if n.Season != nil {
			t.Season = *n.Season
		}
		if n.Episode != nil {
			t.Episode = *n.Episode
		}
	}
	rows, err := q.QueryContext(ctx, `SELECT lf.path, f.relative_path FROM slots s JOIN files f ON f.id = s.file_id
		JOIN versions v ON v.id = f.version_id JOIN library_folders lf ON lf.id = v.library_folder_id
		WHERE s.item_id = ? AND f.state = 'present' ORDER BY v.is_main DESC, f.id`, itemID)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	for rows.Next() {
		var root, rel string
		if err := rows.Scan(&root, &rel); err != nil {
			return t, err
		}
		t.Paths = append(t.Paths, filepath.Join(root, rel))
	}
	return t, rows.Err()
}
