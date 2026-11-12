package importer

import (
	"strings"
	"testing"
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

// HolyArrs grabbed "<Title> Part I (year)" for library "<Title> (year)":
// it is placed even though no library name says "Part I".
func TestPlanAcceptsTheReleaseHolyArrsGrabbed(t *testing.T) {
	tg := godfather()
	tg.Grabbed = true
	m, c, why := onlyStep(t, tg, godfatherPartI)
	if m != "hardlink" || c < MinConfidence || !strings.Contains(why, "HolyArrs grabbed") {
		t.Fatalf("method %s conf %v why %q", m, c, why)
	}
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

// A file HolyArrs did not grab, whose name matches no library name, is
// still refused.
func TestPlanStillRefusesAMismatchNotGrabbed(t *testing.T) {
	m, _, why := onlyStep(t, godfather(), godfatherPartI)
	if m != "refuse" || !strings.Contains(why, "does not match the Title") {
		t.Fatalf("method %s why %q", m, why)
	}
}

// Even a grab is refused when the year is plainly another film's.
func TestPlanRefusesAGrabWithAnotherYear(t *testing.T) {
	tg := godfather()
	tg.Grabbed = true
	m, _, why := onlyStep(t, tg, "/dl/The Godfather Part III (1990) 1080p BluRay x264/The Godfather Part III (1990) 1080p BluRay x264.mkv")
	if m != "refuse" || !strings.Contains(why, "does not match the Title") {
		t.Fatalf("method %s why %q", m, why)
	}
}

// The other safety checks still run on a grab.
func TestPlanGrabStillRefusesASample(t *testing.T) {
	tg := godfather()
	tg.Grabbed = true
	steps := Plan([]Candidate{{Path: "/dl/x/The Godfather Part I (1972) 1080p-sample.mkv", Size: 20 << 20}}, tg)
	if len(steps) != 1 || steps[0].Method != "refuse" || !strings.Contains(strings.Join(steps[0].Why, " "), "sample") {
		t.Fatalf("steps %+v", steps)
	}
}
