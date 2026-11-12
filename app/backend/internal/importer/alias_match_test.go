package importer

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	domain "github.com/HolyArrs/holyarrs/app/backend/internal/core/model"
)

const godfatherPartI = "/dl/The Godfather Part I (1972) REMASTERED 1080p 10bit BluRay x265 10bit HEVC [Org Hindi DVD DD 5 1 ~ 224kbps + English AAC 5 1] ESub ~ Joy/The Godfather Part I (1972) REMASTERED 1080p 10bit BluRay x265 HEVC.mkv"

func godfather() Target {
	return Target{LibraryPath: "/lib", FolderName: "The Godfather (1972)", TitleID: 1, TitleName: "The Godfather", Year: 1972, SlotIDs: []int64{1}}
}

func onlyStep(t *testing.T, tg Target, path string) (method string, conf float64, why string) {
	t.Helper()
	steps := Plan([]Candidate{{Path: path, Size: 8 << 30}}, tg)
	if len(steps) != 1 {
		t.Fatalf("got %d steps: %+v", len(steps), steps)
	}
	return steps[0].Method, steps[0].Confidence, strings.Join(steps[0].Why, "; ")
}

// The Title's aliases count, as importing by hand does: "The Godfather
// Part I" is one of The Godfather's aliases.
func TestPlanMatchesATitleAlias(t *testing.T) {
	tg := godfather()
	tg.Aliases = []string{"Il Padrino", "The Godfather Part I"}
	m, c, why := onlyStep(t, tg, godfatherPartI)
	if m != "hardlink" || c != 1 || !strings.Contains(why, "by clean name and year") {
		t.Fatalf("method %s conf %v why %q", m, c, why)
	}
}

// A name that is neither the Title's name nor an alias is still refused.
func TestPlanRefusesANameThatIsNoAlias(t *testing.T) {
	m, _, why := onlyStep(t, godfather(), godfatherPartI)
	if m != "refuse" || !strings.Contains(why, "does not match the Title") {
		t.Fatalf("method %s why %q", m, why)
	}
	tg := godfather()
	tg.Aliases = []string{"The Godfather Part I"}
	for _, p := range []string{"/dl/x/The Godfather Part II 1080p BluRay x264.mkv", "/dl/x/Scarface 1080p BluRay x264.mkv",
		"/dl/x/The Godfather Part III (1990) 1080p BluRay x264.mkv"} {
		if m, _, why := onlyStep(t, tg, p); m != "refuse" {
			t.Errorf("%s: method %s why %q", filepath.Base(p), m, why)
		}
	}
}

// aliasWorld is the import world with the download's file renamed to "Dune
// Part II", a name only an alias of "Dune: Part Two" can match.
func aliasWorld(t *testing.T, aliases string) world {
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
		_, err := tx.Exec("UPDATE titles SET aliases = ?", aliases)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// Import reads the aliases from the Title itself.
func TestImportMatchesTheTitlesAliases(t *testing.T) {
	w := aliasWorld(t, `["Dune Part II"]`)
	imp, err := (&Importer{Store: w.s}).Import(context.Background(), w.dlID, true, domain.System)
	if err != nil {
		t.Fatal(err)
	}
	if len(imp.Plan) != 1 || imp.Plan[0].Method != "hardlink" || imp.Plan[0].Confidence != 1 {
		t.Fatalf("plan %+v", imp.Plan)
	}
}

func TestImportRefusesWithoutTheAlias(t *testing.T) {
	w := aliasWorld(t, `[]`)
	imp, err := (&Importer{Store: w.s}).Import(context.Background(), w.dlID, true, domain.System)
	if err != nil {
		t.Fatal(err)
	}
	if len(imp.Plan) != 1 || imp.Plan[0].Method != "refuse" {
		t.Fatalf("plan %+v", imp.Plan)
	}
}
