package downloads

import (
	"context"
	"database/sql"
	"log"

	domain "github.com/HolyArrs/holyarrs/app/backend/internal/core/model"
	"github.com/HolyArrs/holyarrs/app/backend/internal/mediafiles"
)

// Custom change (HolyArrs Custom, WP-052). A pack (an album, a season) is
// chosen for several Slots at once, but its grab Job has only one of them as
// its subject. When the grab ended without a send, giveUp freed that one Slot
// and left the others chosen for a Release that will never come, so the album
// could not be picked again. freePackTx frees the rest; healStuckPicks frees
// Slots left that way before this fix.

// freePackTx moves every other Slot still chosen for releaseID back to wanted
// and returns their ids. Slots that chose another Release, or moved on, are
// left alone.
func freePackTx(ctx context.Context, tx *sql.Tx, releaseID, except int64, why string, by domain.Actor) ([]int64, error) {
	return movePackTx(ctx, tx, releaseID, except, mediafiles.SlotWanted, true, why, by)
}

// movePackTx moves every other Slot still chosen for releaseID to state; clear
// drops their chosen Release as giveUp does for the subject.
func movePackTx(ctx context.Context, tx *sql.Tx, releaseID, except int64, state mediafiles.SlotState, clear bool, why string, by domain.Actor) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id FROM slots WHERE state = 'chosen' AND chosen_release_id = ? AND id != ? ORDER BY id", releaseID, except)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if clear {
			if _, err := tx.ExecContext(ctx, "UPDATE slots SET chosen_release_id = NULL WHERE id = ? AND state = 'chosen' AND chosen_release_id = ?", id, releaseID); err != nil {
				return nil, err
			}
		}
		if err := mediafiles.MoveSlotTx(tx, id, state, why, by); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// stuckPicks finds Slots chosen for a Release whose grab ended without a send:
// no grab Job for it is queued or running, one failed or ended "not sent" and
// its own Slot was given up, and no Download was ever made from it.
const stuckPicks = `SELECT s.id, s.chosen_release_id FROM slots s
WHERE s.state = 'chosen' AND s.chosen_release_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.kind = 'grab' AND json_extract(j.params, '$.release_id') = s.chosen_release_id
      AND j.state IN ('queued', 'running'))
  AND EXISTS (SELECT 1 FROM jobs j WHERE j.kind = 'grab' AND json_extract(j.params, '$.release_id') = s.chosen_release_id
      AND (j.state IN ('failed', 'cancelled') OR (j.state = 'done' AND json_extract(j.result, '$.not_sent') IS NOT NULL))
      -- Only when the grab's own Slot was given up (back to wanted or moved on).
      -- A Slot still chosen for the Release is an uncertain send left for a
      -- person; one waiting on a decision (tags) keeps its pack with it.
      AND json_extract(j.subject, '$.type') = 'slot'
      AND NOT EXISTS (SELECT 1 FROM slots g WHERE g.id = json_extract(j.subject, '$.id')
          AND ((g.state = 'chosen' AND g.chosen_release_id = s.chosen_release_id) OR g.state = 'needs_a_decision')))
  AND NOT EXISTS (SELECT 1 FROM downloads d WHERE d.release_id = s.chosen_release_id)
ORDER BY s.id`

// healStuckPicks frees Slots left chosen by a failed pack grab before
// freePackTx existed. It runs once when the Manager starts.
func (m *Manager) healStuckPicks(ctx context.Context) {
	// Look first on a read connection: almost always nothing is stuck, and a
	// write at start would hold the store while the readers start.
	var stuck int64
	if err := m.Store.Read().QueryRowContext(ctx, "SELECT COUNT(*) FROM ("+stuckPicks+")").Scan(&stuck); err != nil || stuck == 0 {
		if err != nil {
			log.Printf("downloads: looking for Slots left chosen by a failed grab: %v", err)
		}
		return
	}
	var freed []int64
	err := m.Store.Write(ctx, func(tx *sql.Tx) error {
		freed = freed[:0]
		rows, err := tx.QueryContext(ctx, stuckPicks)
		if err != nil {
			return err
		}
		type pick struct{ slot, rel int64 }
		var ps []pick
		for rows.Next() {
			var p pick
			if err := rows.Scan(&p.slot, &p.rel); err != nil {
				rows.Close()
				return err
			}
			ps = append(ps, p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, p := range ps {
			res, err := tx.ExecContext(ctx, "UPDATE slots SET chosen_release_id = NULL WHERE id = ? AND state = 'chosen' AND chosen_release_id = ?", p.slot, p.rel)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				continue
			}
			if err := mediafiles.MoveSlotTx(tx, p.slot, mediafiles.SlotWanted, "not sent: the grab of its pack ended without a send", domain.System); err != nil {
				return err
			}
			freed = append(freed, p.slot)
		}
		return nil
	})
	if err != nil {
		log.Printf("downloads: freeing Slots left chosen by a failed grab: %v", err)
		return
	}
	if len(freed) > 0 {
		log.Printf("downloads: freed %d Slots left chosen by a failed grab: %v", len(freed), freed)
		for _, id := range freed {
			m.publish("slot", map[string]any{"id": id, "state": mediafiles.SlotWanted})
		}
	}
}

// packNeedsDecision moves the rest of a pack to needs a decision with its grab
// Slot, when no Downloader takes the title's tags.
func (m *Manager) packNeedsDecision(ctx context.Context, slotID, releaseID int64, why string, by domain.Actor) {
	var ids []int64
	err := m.Store.Write(ctx, func(tx *sql.Tx) error {
		var st string
		if err := tx.QueryRowContext(ctx, "SELECT state FROM slots WHERE id = ?", slotID).Scan(&st); err != nil || st != string(mediafiles.SlotNeedsADecision) {
			return err // the grab Slot did not move: leave the pack as it is
		}
		var err error
		ids, err = movePackTx(ctx, tx, releaseID, slotID, mediafiles.SlotNeedsADecision, false, why, by)
		return err
	})
	if err != nil {
		log.Printf("downloads: moving the rest of a pack to needs a decision: %v", err)
		return
	}
	for _, id := range ids {
		m.publish("slot", map[string]any{"id": id, "state": mediafiles.SlotNeedsADecision, "reason": why})
	}
}
