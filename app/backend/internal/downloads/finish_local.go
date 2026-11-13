package downloads

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	domain "github.com/HolyArrs/holyarrs/app/backend/internal/core/model"
)

// Custom change (HolyArrs Custom, WP-049). Decypharr, a debrid bridge with a
// qBittorrent API, differs from qBittorrent in two ways that made a finished
// torrent fail its import with "the files were removed by another app":
//
//   - torrents/files lists bare file names, without the torrent's folder,
//     while the files are written inside content_path. Joined to the save
//     folder they name files that never exist.
//   - In download mode it can call a torrent finished while it is still
//     copying the files from the debrid service to this disk.
//
// localFinish fixes the names when the files are found under content_path,
// and holds a finished torrent as downloading while its files are missing
// or smaller than reported on a disk HolyArrs can see, for at most
// finishGrace. A Downloader on another machine (save folder not visible
// here) is left as it was.

// finishGrace is how long a finished torrent may wait for its files.
const finishGrace = 30 * time.Minute

var finishWait sync.Map // download id -> time first held

// localFinish returns j with its file names rooted where the files are, and
// whether the files are still being written so the import should wait.
func localFinish(j Job, id int64, pm []domain.PathMap, now time.Time) (Job, bool) {
	if len(j.Files) == 0 || j.SavePath == "" {
		finishWait.Delete(id)
		return j, false
	}
	save := filepath.Clean(mapLocal(j.SavePath, pm))
	if fi, err := os.Stat(save); err != nil || !fi.IsDir() {
		finishWait.Delete(id)
		return j, false
	}
	j.Files = rootFiles(j, save, pm)
	if filesReady(save, j.Files) {
		finishWait.Delete(id)
		return j, false
	}
	first, _ := finishWait.LoadOrStore(id, now)
	if now.Sub(first.(time.Time)) >= finishGrace {
		finishWait.Delete(id)
		return j, false
	}
	return j, true
}

// rootFiles prefixes bare file names with content_path's folder when that is
// where they are and the save folder does not have them.
func rootFiles(j Job, save string, pm []domain.PathMap) []File {
	if j.ContentPath == "" {
		return j.Files
	}
	content := filepath.Clean(mapLocal(j.ContentPath, pm))
	rel, err := filepath.Rel(save, content)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return j.Files
	}
	if fi, err := os.Stat(content); err != nil || !fi.IsDir() {
		return j.Files
	}
	prefix := filepath.ToSlash(rel) + "/"
	atSave, atContent := 0, 0
	for _, f := range j.Files {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		if strings.HasPrefix(name, prefix) {
			return j.Files // already rooted
		}
		if _, err := os.Lstat(filepath.Join(save, filepath.FromSlash(name))); err == nil {
			atSave++
		}
		if _, err := os.Lstat(filepath.Join(content, filepath.FromSlash(name))); err == nil {
			atContent++
		}
	}
	if atSave > 0 || atContent == 0 {
		return j.Files
	}
	out := make([]File, len(j.Files))
	for i, f := range j.Files {
		out[i] = f
		out[i].Name = path.Join(filepath.ToSlash(rel), strings.ReplaceAll(f.Name, `\`, "/"))
	}
	return out
}

// filesReady reports whether every wanted file is on disk at its full size.
func filesReady(save string, files []File) bool {
	for _, f := range files {
		if f.Priority == 0 && f.Progress < 1 {
			continue // skipped in the client
		}
		fi, err := os.Stat(filepath.Join(save, filepath.FromSlash(strings.ReplaceAll(f.Name, `\`, "/"))))
		if err != nil || !fi.Mode().IsRegular() || (f.Size > 0 && fi.Size() < f.Size) {
			return false
		}
	}
	return true
}

// seenRenamed reports whether the file names changed with the same count,
// which the poll's change check would otherwise miss.
func seenRenamed(a, b []domain.SeenFile) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			return true
		}
	}
	return false
}
