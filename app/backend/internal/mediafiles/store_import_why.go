package mediafiles

import (
	"context"
	"strings"

	"github.com/HolyArrs/holyarrs/app/backend/internal/core/database"
)

// RefusedWhy is the reason the newest import plan of a Download refused
// its file, so the review card can say what really happened. It is "" when
// there is no plan or no refused step.
func RefusedWhy(ctx context.Context, q database.Lister, downloadID int64) string {
	is, err := database.Select[Import](ctx, q, "imports", "download_id = ? ORDER BY id DESC LIMIT 1", downloadID)
	if err != nil || len(is) == 0 {
		return ""
	}
	for _, s := range is[0].Plan {
		if s.Method == "refuse" && len(s.Why) > 0 {
			return strings.Join(s.Why, "; ")
		}
	}
	return ""
}
