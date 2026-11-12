package importer

import (
	"fmt"

	"github.com/HolyArrs/holyarrs/app/backend/internal/core/releasename"
	"github.com/HolyArrs/holyarrs/app/backend/internal/mediafiles"
)

// An import plan matches each file to the Title by its parsed name and the
// Title's name and aliases, as importing by hand does. A Download HolyArrs
// grabbed itself is already tied to this Title's slot: the Release was
// picked for it, often found by the Title's IDs, so its name may differ
// from every name the library knows ("The Godfather Part I" for "The
// Godfather"). For such a Download a name mismatch alone does not refuse
// the file. The year still has to agree, and every other check (programs,
// samples, the main file, editions, upgrades, space) still runs. A file
// HolyArrs did not grab keeps the strict name match.

// grabbedMatch returns m unchanged unless the Download is HolyArrs' own
// grab and the parsed year agrees with the Title (same year, one off, or
// none given). Then it matches the Title with a "likely" confidence.
func (t Target) grabbedMatch(p releasename.Result, m mediafiles.Match, ref *mediafiles.TitleRef) mediafiles.Match {
	if !t.Grabbed || m.Title != nil || p.IsShow() || p.IsMusic() {
		return m
	}
	y := p.Year.Value
	if y != 0 && t.Year != 0 && (y-t.Year > 1 || t.Year-y > 1) {
		return m
	}
	return mediafiles.Match{Title: ref, Conf: releasename.Likely,
		Detail: fmt.Sprintf("%s (%d): HolyArrs grabbed this Release for it; the name says %q", t.TitleName, t.Year, p.Title.Value)}
}
