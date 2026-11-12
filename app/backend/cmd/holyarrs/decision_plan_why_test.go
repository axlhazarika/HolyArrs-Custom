package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/HolyArrs/holyarrs/app/backend/internal/core/database"
	domain "github.com/HolyArrs/holyarrs/app/backend/internal/core/model"
	"github.com/HolyArrs/holyarrs/app/backend/internal/downloads"
	"github.com/HolyArrs/holyarrs/app/backend/internal/mediafiles"
)

// A movie whose import plan refused the file shows the plan's reason on the
// review card, not a guess that the edition is unknown.
func TestNeedsDecisionShowsTheImportPlanReason(t *testing.T) {
	x := seed41(t, mediafiles.SlotNeedsADecision, "failed")
	exec41(t, x.s, "UPDATE slots SET last_reason = ? WHERE id = ?", "nothing could be placed; look at the import plan", x.slotID)
	list := func() downloads.DecisionView {
		var out struct {
			Items []downloads.DecisionView `json:"items"`
		}
		_ = json.Unmarshal(do(t, "GET", x.srv.URL+"/api/v1/needs-decision", "", 200), &out)
		if len(out.Items) != 1 {
			t.Fatalf("%+v", out)
		}
		return out.Items[0]
	}
	if it := list(); it.Why != "conflict" || it.PlanWhy != "" {
		t.Fatalf("before any plan: %+v", it)
	}
	dl := x.dlID
	why := `does not match the Title: no Title in the library is called "Salt Choir Part I"`
	if err := x.s.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := database.Insert(tx, "imports", &mediafiles.Import{DownloadID: &dl, FromPath: "/dl/x", Kind: "movie", Mode: "auto", State: "failed",
			Plan:   []mediafiles.PlanStep{{SourceFile: "/dl/x/a.mkv", TargetSlotIDs: []int64{}, Method: "refuse", Why: []string{why}}},
			Result: []mediafiles.PlaceResult{}}, domain.System)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if it := list(); it.PlanWhy != why || strings.Contains(strings.ToLower(it.PlanWhy), "edition") {
		t.Fatalf("plan why %q", it.PlanWhy)
	}
}
