package downloads

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HolyArrs/holyarrs/app/backend/internal/core/database"
	domain "github.com/HolyArrs/holyarrs/app/backend/internal/core/model"
)

// decypharrJob is a torrent as Decypharr reports it: finished, its file named
// bare while the file lives inside content_path's folder.
func decypharrJob(save string) Job {
	return Job{ID: ownHash, Name: "Nosferatu (1922) (BFI)", Category: "holyarrs", State: StateFinished, Progress: 1, Size: 10,
		SavePath: save, ContentPath: filepath.Join(save, "Nosferatu.1922-HeVK"),
		Files: []File{{Name: "Nosferatu.1922-HeVK.mkv", Size: 10, Progress: 1, Priority: 1}}}
}

func finishRig(t *testing.T, j Job, seen []domain.SeenFile) (*rig, func() domain.Download) {
	t.Helper()
	r := newRig(t, j)
	r.m.Sync = nil
	ctx := context.Background()
	if err := r.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE downloaders SET adapter = 'qbittorrent' WHERE id = ?", r.dlID)
		if err != nil {
			return err
		}
		if seen != nil {
			var d domain.Download
			if err := database.Get(ctx, tx, "downloads", 1, &d); err != nil {
				return err
			}
			d.FilesSeen, d.State, d.Progress = seen, "downloading", 0.9
			return database.Update(tx, "downloads", &d, domain.System)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { finishWait.Delete(int64(1)) })
	return r, func() domain.Download {
		var d domain.Download
		if err := database.Get(ctx, r.db.Read(), "downloads", 1, &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
}

// The Nosferatu case: Decypharr calls the torrent finished while it is still
// copying the file, and names the file without its folder. HolyArrs waited
// for nothing and the import found no file at <save>/<name>: "removed by
// another app". Now the download waits until the file is whole, then is
// finished with the file named where it is.
func TestFinish_DecypharrWaitsForTheFileAndRootsItsName(t *testing.T) {
	save := t.TempDir()
	dir := filepath.Join(save, "Nosferatu.1922-HeVK")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "Nosferatu.1922-HeVK.mkv")
	if err := os.WriteFile(file, []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, get := finishRig(t, decypharrJob(save), []domain.SeenFile{{Name: "Nosferatu.1922-HeVK.mkv", Size: 10, Progress: 1}})
	ctx := context.Background()
	if _, err := r.m.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if d := get(); d.State == "finished" {
		t.Fatalf("finished while the file is still being written: %+v", d)
	}
	if err := os.WriteFile(file, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	d := get()
	if d.State != "finished" {
		t.Fatalf("state %q once the file is whole, want finished", d.State)
	}
	if len(d.FilesSeen) != 1 || d.FilesSeen[0].Name != "Nosferatu.1922-HeVK/Nosferatu.1922-HeVK.mkv" {
		t.Fatalf("files seen %+v, want the name under its folder", d.FilesSeen)
	}
	if _, err := os.Stat(filepath.Join(d.SavePath, filepath.FromSlash(d.FilesSeen[0].Name))); err != nil {
		t.Fatalf("the recorded file does not exist: %v", err)
	}
}

// A qBittorrent that names files where they are is left alone.
func TestFinish_FilesAtTheSaveFolderUnchanged(t *testing.T) {
	save := t.TempDir()
	j := decypharrJob(save)
	j.ContentPath = filepath.Join(save, "Nosferatu.1922-HeVK.mkv")
	if err := os.WriteFile(j.ContentPath, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, wait := localFinish(j, 1, nil, time.Now())
	if wait || got.Files[0].Name != "Nosferatu.1922-HeVK.mkv" {
		t.Fatalf("wait %v, files %+v", wait, got.Files)
	}
}

// A Downloader whose save folder is not on this machine is never held.
func TestFinish_RemoteSaveFolderNeverWaits(t *testing.T) {
	j := decypharrJob("/no/such/folder/on/this/mac")
	if _, wait := localFinish(j, 1, nil, time.Now()); wait {
		t.Fatal("held a torrent whose files HolyArrs cannot see")
	}
}

// Files that never arrive are let through after the grace, so the import
// reports what it finds instead of the download waiting for ever.
func TestFinish_GiveUpWaitingAfterTheGrace(t *testing.T) {
	t.Cleanup(func() { finishWait.Delete(int64(1)) })
	j := decypharrJob(t.TempDir())
	now := time.Now()
	if _, wait := localFinish(j, 1, nil, now); !wait {
		t.Fatal("missing file not waited for")
	}
	if _, wait := localFinish(j, 1, nil, now.Add(finishGrace-time.Second)); !wait {
		t.Fatal("stopped waiting before the grace")
	}
	if _, wait := localFinish(j, 1, nil, now.Add(finishGrace)); wait {
		t.Fatal("still waiting after the grace")
	}
}

func TestFinish_SeenRenamed(t *testing.T) {
	a := []domain.SeenFile{{Name: "a.mkv"}}
	if seenRenamed(a, a) || !seenRenamed(a, []domain.SeenFile{{Name: "x/a.mkv"}}) || seenRenamed(a, nil) {
		t.Fatal("seenRenamed wrong")
	}
}
