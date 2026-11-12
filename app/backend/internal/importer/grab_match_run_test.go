package importer

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	domain "github.com/HolyArrs/holyarrs/app/backend/internal/core/model"
)

// misnamedWorld is the import world with the download's file renamed to a
// name no library name matches ("Dune Part II" for "Dune: Part Two"). grabbed
// links the Download to a Release, as HolyArrs' own grab does.
func misnamedWorld(t *testing.T, grabbed bool) world {
	t.Helper()
	w := setup(t)
	const name = "Dune.Part.II.2024.1080p.WEB.H264-HOLYARRS.mkv"
	if err := os.Rename(filepath.Join(w.dl, relName+".mkv"), filepath.Join(w.dl, name)); err != nil {
		t.Fatal(err)
	}
	err := w.s.Write(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE downloads SET files_seen = json_array(json_object('name', ?, 'size', 2048)) WHERE id = ?`, name, w.dlID); err != nil {
			return err
		}
		if !grabbed {
			return nil
		}
		var src, rel int64
		if err := tx.QueryRow(`INSERT INTO sources (created_at, created_by, updated_at, updated_by, name, protocol, format, url)
			VALUES ('2026-01-01T00:00:00Z', '{"kind":"system"}', '2026-01-01T00:00:00Z', '{"kind":"system"}', 'Lab', 'torrent', 'torznab', 'http://lab') RETURNING id`).Scan(&src); err != nil {
			return err
		}
		if err := tx.QueryRow(`INSERT INTO releases (created_at, created_by, updated_at, updated_by, source_id, name, link_ref, protocol, found_by, state)
			VALUES ('2026-01-01T00:00:00Z', '{"kind":"system"}', '2026-01-01T00:00:00Z', '{"kind":"system"}', ?, ?, 'x', 'torrent', 'auto_search', 'chosen') RETURNING id`, src, name).Scan(&rel); err != nil {
			return err
		}
		_, err := tx.Exec("UPDATE downloads SET release_id = ? WHERE id = ?", rel, w.dlID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestImportPlacesHolyArrsOwnGrabDespiteItsName(t *testing.T) {
	w := misnamedWorld(t, true)
	imp, err := (&Importer{Store: w.s}).Import(context.Background(), w.dlID, true, domain.System)
	if err != nil {
		t.Fatal(err)
	}
	if len(imp.Plan) != 1 || imp.Plan[0].Method == "refuse" || imp.Plan[0].Method == "skip" {
		t.Fatalf("plan %+v", imp.Plan)
	}
}

func TestImportRefusesAMisnamedFileHolyArrsDidNotGrab(t *testing.T) {
	w := misnamedWorld(t, false)
	imp, err := (&Importer{Store: w.s}).Import(context.Background(), w.dlID, true, domain.System)
	if err != nil {
		t.Fatal(err)
	}
	if len(imp.Plan) != 1 || imp.Plan[0].Method != "refuse" {
		t.Fatalf("plan %+v", imp.Plan)
	}
}
