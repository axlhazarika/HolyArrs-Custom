package downloads

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/HolyArrs/holyarrs/app/backend/internal/core/database"
	domain "github.com/HolyArrs/holyarrs/app/backend/internal/core/model"
	"github.com/HolyArrs/holyarrs/app/backend/internal/mediafiles"
)

// chosenPack adds two more Slots chosen for the world's Release (the rest of a
// pack, as an album's tracks are) and one Slot chosen for another Release.
func chosenPack(t *testing.T, w *world) (pack []int64, other int64) {
	t.Helper()
	if err := w.s.Write(context.Background(), func(tx *sql.Tx) error {
		var sl mediafiles.Slot
		if err := database.Get(context.Background(), tx, "slots", w.slotID, &sl); err != nil {
			return err
		}
		add := func(rel int64) (int64, error) {
			it := &mediafiles.Item{TitleID: 1, Kind: "movie"}
			if _, err := database.Insert(tx, "items", it, domain.System); err != nil {
				return 0, err
			}
			s := &mediafiles.Slot{ItemID: it.ID, VersionID: sl.VersionID, State: mediafiles.SlotChosen}
			if _, err := database.Insert(tx, "slots", s, domain.System); err != nil {
				return 0, err
			}
			_, err := tx.Exec("UPDATE slots SET chosen_release_id = ? WHERE id = ?", rel, s.ID)
			return s.ID, err
		}
		for range 2 {
			id, err := add(w.relID)
			if err != nil {
				return err
			}
			pack = append(pack, id)
		}
		var src int64
		if err := tx.QueryRow("SELECT source_id FROM releases WHERE id = ?", w.relID).Scan(&src); err != nil {
			return err
		}
		rel := &domain.Release{SourceID: src, Name: "Other", LinkRef: "http://x/o.torrent", Protocol: "torrent", Flags: []string{}, Parsed: map[string]any{},
			MatchedSlotIDs: []int64{}, Verdict: map[string]any{}, FoundBy: "auto_search", State: "chosen"}
		if _, err := database.Insert(tx, "releases", rel, domain.System); err != nil {
			return err
		}
		var err error
		other, err = add(rel.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return pack, other
}

func chosenState(t *testing.T, w *world, id int64) (string, bool) {
	t.Helper()
	var st string
	var rel sql.NullInt64
	if err := w.s.Read().QueryRow("SELECT state, chosen_release_id FROM slots WHERE id = ?", id).Scan(&st, &rel); err != nil {
		t.Fatal(err)
	}
	return st, rel.Valid
}

// A pack whose grab can never be sent frees every Slot it was chosen for, not
// only the grab Job's subject; a Slot that chose another Release stays.
func TestGiveUpFreesTheWholePack(t *testing.T) {
	w, m := setup(t)
	pack, other := chosenPack(t, w)
	gone := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(gone.Close)
	w.exec(t, "UPDATE releases SET info_hash = NULL, link_ref = ? WHERE id = ?", gone.URL+"/decoy.torrent", w.relID)
	w.exec(t, "UPDATE sources SET allow_local_network = 1, url = ?", gone.URL+"/api")
	if err := m.HandleGrab(context.Background(), grabRun(w, 1)); err != nil {
		t.Fatalf("the Job should end: %v", err)
	}
	for _, id := range append([]int64{w.slotID}, pack...) {
		if st, has := chosenState(t, w, id); st != string(mediafiles.SlotWanted) || has {
			t.Errorf("slot %d is %s (chosen release kept: %v), want wanted", id, st, has)
		}
	}
	if st, has := chosenState(t, w, other); st != string(mediafiles.SlotChosen) || !has {
		t.Errorf("slot %d of another Release is %s, want left chosen", other, st)
	}
}

// The non-final path (the Downloader failed on the last try) frees the pack too:
// this is the live Decypharr case, "download not found in the Downloader".
func TestGiveUpAfterLastTryFreesThePack(t *testing.T) {
	w, m := setup(t)
	pack, _ := chosenPack(t, w)
	m.giveUp(context.Background(), w.slotID, w.relID, "sending: download not found in the Downloader", false, domain.System)
	for _, id := range append([]int64{w.slotID}, pack...) {
		if st, has := chosenState(t, w, id); st != string(mediafiles.SlotWanted) || has {
			t.Errorf("slot %d is %s, want wanted", id, st)
		}
	}
}

// Slots left chosen by a failed grab before the fix are freed once at start;
// a pick whose grab is still queued, or that made a Download, is left alone.
func TestHealStuckPicks(t *testing.T) {
	w, m := setup(t)
	pack, other := chosenPack(t, w)
	// The world's subject Slot was already freed by the old giveUp.
	w.exec(t, "UPDATE slots SET state = 'wanted', chosen_release_id = NULL WHERE id = ?", w.slotID)
	w.exec(t, `INSERT INTO jobs (created_at, created_by, updated_at, updated_by, kind, subject, params, state, run_after, asked_by, error)
		VALUES ('2026-11-14T08:52:00Z', '{"kind":"system"}', '2026-11-14T08:55:00Z', '{"kind":"system"}', 'grab', '{"type":"slot","id":1}', json_object('release_id', ?), 'failed', '2026-11-14T08:52:00Z', '{"kind":"system"}', '{"message":"download not found"}')`, w.relID)
	var otherRel int64
	if err := w.s.Read().QueryRow("SELECT chosen_release_id FROM slots WHERE id = ?", other).Scan(&otherRel); err != nil {
		t.Fatal(err)
	}
	w.exec(t, `INSERT INTO jobs (created_at, created_by, updated_at, updated_by, kind, subject, params, state, run_after, asked_by)
		VALUES ('2026-11-14T08:52:00Z', '{"kind":"system"}', '2026-11-14T08:52:00Z', '{"kind":"system"}', 'grab', '{"type":"slot","id":9}', json_object('release_id', ?), 'queued', '2026-11-14T08:52:00Z', '{"kind":"system"}')`, otherRel)
	m.healStuckPicks(context.Background())
	for _, id := range pack {
		if st, has := chosenState(t, w, id); st != string(mediafiles.SlotWanted) || has {
			t.Errorf("stuck slot %d is %s, want wanted", id, st)
		}
	}
	if st, _ := chosenState(t, w, other); st != string(mediafiles.SlotChosen) {
		t.Errorf("slot %d with a queued grab is %s, want left chosen", other, st)
	}
	m.healStuckPicks(context.Background()) // nothing left: no error, no change
}

// failedGrab records a grab Job for the world's Release that failed on its
// last attempt, with the world's Slot as its subject.
func failedGrab(t *testing.T, w *world) {
	t.Helper()
	w.exec(t, `INSERT INTO jobs (created_at, created_by, updated_at, updated_by, kind, subject, params, state, run_after, asked_by, error)
		VALUES ('2026-11-14T08:52:00Z', '{"kind":"system"}', '2026-11-14T08:55:00Z', '{"kind":"system"}', 'grab', json_object('type', 'slot', 'id', ?), json_object('release_id', ?), 'failed', '2026-11-14T08:52:00Z', '{"kind":"system"}', '{"message":"uncertain"}')`, w.slotID, w.relID)
}

// An uncertain last attempt leaves the grab's own Slot chosen for a person
// (upstream: "An unknown outcome stays for a person"); the heal must leave
// that pack alone, or the album could be downloaded twice.
func TestHealLeavesAnUncertainGrabAlone(t *testing.T) {
	w, m := setup(t)
	pack, _ := chosenPack(t, w)
	failedGrab(t, w) // the world's Slot is still chosen for the Release
	m.healStuckPicks(context.Background())
	for _, id := range append([]int64{w.slotID}, pack...) {
		if st, has := chosenState(t, w, id); st != string(mediafiles.SlotChosen) || !has {
			t.Errorf("slot %d is %s after an uncertain grab, want left chosen", id, st)
		}
	}
}

// When no Downloader takes the title's tags, the grab's Slot waits on a
// decision and the rest of its pack waits with it; the heal leaves them.
func TestPackWaitsOnTheDecisionWithItsGrabSlot(t *testing.T) {
	w, m := setup(t)
	pack, other := chosenPack(t, w)
	ctx := context.Background()
	if err := mediafiles.MoveSlot(ctx, w.s, w.slotID, mediafiles.SlotNeedsADecision, "no Downloader takes the tags", domain.System); err != nil {
		t.Fatal(err)
	}
	m.packNeedsDecision(ctx, w.slotID, w.relID, "no Downloader takes the tags", domain.System)
	for _, id := range pack {
		if st, _ := chosenState(t, w, id); st != string(mediafiles.SlotNeedsADecision) {
			t.Errorf("pack slot %d is %s, want needs_a_decision", id, st)
		}
	}
	if st, _ := chosenState(t, w, other); st != string(mediafiles.SlotChosen) {
		t.Errorf("slot %d of another Release is %s, want left chosen", other, st)
	}
	failedGrab(t, w)
	m.healStuckPicks(ctx)
	for _, id := range pack {
		if st, _ := chosenState(t, w, id); st != string(mediafiles.SlotNeedsADecision) {
			t.Errorf("heal moved pack slot %d to %s", id, st)
		}
	}
}

// The grab Slot did not move (it moved on meanwhile): the pack is left alone.
func TestPackStaysWhenTheGrabSlotDidNotWait(t *testing.T) {
	w, m := setup(t)
	pack, _ := chosenPack(t, w)
	m.packNeedsDecision(context.Background(), w.slotID, w.relID, "tags", domain.System)
	for _, id := range pack {
		if st, _ := chosenState(t, w, id); st != string(mediafiles.SlotChosen) {
			t.Errorf("pack slot %d is %s, want left chosen", id, st)
		}
	}
}
