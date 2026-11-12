package connections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Decypharr is where debrid accounts go: its config file, and how to restart
// it and see what it listens on. ConfigPath empty means not set up; the
// accounts are then kept here and marked pending.
type Decypharr struct {
	ConfigPath string
	// Label is the launchd label Decypharr runs under.
	Label string
	// Restart and Listeners default to launchctl and lsof; tests replace them.
	Restart   func(ctx context.Context) error
	Stop      func(ctx context.Context) error
	Listeners func(ctx context.Context) ([]string, error)
	// Wait is how long to wait for Decypharr to listen again after a restart.
	Wait time.Duration
}

// keepBackups is how many config backups are kept next to the file.
const keepBackups = 5

var (
	errNoDecypharr = errors.New("Decypharr is not set up for HolyArrs: start HolyArrs with HOLYARRS_DECYPHARR_CONFIG and HOLYARRS_DECYPHARR_LABEL")
	// ErrUnsafeConfig means the config's bind address or Hearsay switches are
	// not the safe ones; nothing is written.
	ErrUnsafeConfig = errors.New("Decypharr's config is not safe to change")
)

// On says whether Decypharr is set up.
func (d *Decypharr) On() bool { return d != nil && d.ConfigPath != "" }

// readConfig reads the config keeping every key and number as it was.
func readConfig(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Decypharr config: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, errors.New("Decypharr's config is not valid JSON; fix it in Decypharr first")
	}
	return m, nil
}

// checkSafe is the standing rule: bind address 127.0.0.1, and Hearsay
// disabled, not participating and not publishing. Anything else, including a
// missing or renamed switch, refuses.
func checkSafe(m map[string]any) error {
	if m["bind_address"] != "127.0.0.1" {
		return fmt.Errorf("%w: bind_address must be 127.0.0.1", ErrUnsafeConfig)
	}
	h, ok := m["hearsay"].(map[string]any)
	if !ok {
		return fmt.Errorf("%w: the hearsay section is missing", ErrUnsafeConfig)
	}
	if h["disabled"] != true || h["participate"] != false || h["publish"] != false {
		return fmt.Errorf("%w: hearsay must be disabled, not participating and not publishing", ErrUnsafeConfig)
	}
	return nil
}

// debridItems is the debrids array of the config.
func debridItems(m map[string]any) []map[string]any {
	raw, _ := m["debrids"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, x := range raw {
		if d, ok := x.(map[string]any); ok {
			out = append(out, d)
		}
	}
	return out
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

// owner returns the entry that wrote item d: its name is the entry's name
// or the name last pushed, and its key is the entry's key or the key last
// pushed. Anything else was made outside HolyArrs.
func owner(d map[string]any, es []Entry) (Entry, bool) {
	name, key := str(d, "name"), str(d, "api_key")
	for _, e := range es {
		if e.Kind != KindDebrid || key == "" {
			continue
		}
		if (name == e.Name || name == e.PushedName && e.PushedName != "") && (key == e.Secret || key == e.PushedKey && e.PushedKey != "") {
			return e, true
		}
	}
	return Entry{}, false
}

// mergeDebrids makes the debrids array hold the enabled accounts of all.
// Items this module wrote are updated in place or dropped when no longer
// wanted (disabled, renamed or in gone); every other item stays exactly as
// it is. A wanted name already used by an outside item is refused.
func mergeDebrids(m map[string]any, all []Entry, gone []Entry) error {
	known := append(append([]Entry{}, all...), gone...)
	want := map[string]Entry{}
	for _, e := range all {
		if e.Kind == KindDebrid && e.Enabled {
			want[e.Name] = e
		}
	}
	var out []any
	done := map[string]bool{}
	for _, d := range debridItems(m) {
		e, ours := owner(d, known)
		if !ours {
			if _, clash := want[str(d, "name")]; clash {
				return fmt.Errorf("Decypharr already has an account named %q that was made outside HolyArrs; give this one another name", str(d, "name"))
			}
			out = append(out, d)
			continue
		}
		if w, ok := want[e.Name]; ok && w.ID == e.ID && !done[e.Name] {
			setDebrid(d, w)
			done[e.Name] = true
			out = append(out, d)
		}
	}
	names := make([]string, 0, len(want))
	for n := range want {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !done[n] {
			d := map[string]any{}
			setDebrid(d, want[n])
			out = append(out, d)
		}
	}
	if out == nil {
		out = []any{}
	}
	m["debrids"] = out
	return nil
}

// setDebrid writes e into item d, keeping every other key. A single
// download key follows the API key; a longer list made in Decypharr keeps
// its other keys.
func setDebrid(d map[string]any, e Entry) {
	old := str(d, "api_key")
	keys, _ := d["download_api_keys"].([]any)
	if len(keys) <= 1 {
		keys = []any{e.Secret}
	} else {
		for i, k := range keys {
			if k == old {
				keys[i] = e.Secret
			}
		}
	}
	d["provider"] = e.Provider
	d["name"] = e.Name
	d["api_key"] = e.Secret
	d["download_api_keys"] = keys
}

// unmanagedDebrids lists the items no entry here wrote.
func unmanagedDebrids(m map[string]any, all []Entry) []Unmanaged {
	var out []Unmanaged
	for _, d := range debridItems(m) {
		if _, ours := owner(d, all); !ours {
			out = append(out, Unmanaged{Target: "decypharr", Name: str(d, "name"), Detail: str(d, "provider")})
		}
	}
	return out
}

// writeConfig writes m in place of path: a backup first, then a temporary
// file in the same folder renamed over it, owner-only.
func writeConfig(path string, m map[string]any, now time.Time) error {
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	bak := fmt.Sprintf("%s.holyarrs-%d.bak", path, now.UnixNano())
	if err := os.WriteFile(bak, old, 0o600); err != nil {
		return fmt.Errorf("back up Decypharr config: %w", err)
	}
	pruneBackups(path)
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-holyarrs-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func pruneBackups(path string) {
	old, _ := filepath.Glob(path + ".holyarrs-*.bak")
	sort.Strings(old)
	for len(old) > keepBackups {
		_ = os.Remove(old[0])
		old = old[1:]
	}
}

// Sync writes the debrid accounts into Decypharr's config and restarts it.
// It refuses, writing nothing, when the config is not safe before or would
// not be safe after. After the restart every listener must be on loopback;
// if one is not, Decypharr is stopped and an error returned.
func (d *Decypharr) Sync(ctx context.Context, all []Entry, gone []Entry) error {
	if !d.On() {
		return errNoDecypharr
	}
	m, err := readConfig(d.ConfigPath)
	if err != nil {
		return err
	}
	if err := checkSafe(m); err != nil {
		return err
	}
	before, _ := json.Marshal(m["debrids"])
	if err := mergeDebrids(m, all, gone); err != nil {
		return err
	}
	after, _ := json.Marshal(m["debrids"])
	if err := checkSafe(m); err != nil {
		return err
	}
	if bytes.Equal(before, after) {
		return nil // nothing to change, no restart
	}
	if err := writeConfig(d.ConfigPath, m, time.Now()); err != nil {
		return fmt.Errorf("write Decypharr config: %w", err)
	}
	back, err := readConfig(d.ConfigPath)
	if err != nil {
		return err
	}
	if err := checkSafe(back); err != nil {
		return err
	}
	return d.restart(ctx)
}

func (d *Decypharr) restart(ctx context.Context) error {
	restart, stop, listeners := d.Restart, d.Stop, d.Listeners
	if restart == nil {
		restart = func(ctx context.Context) error { return launchctl(ctx, "kickstart", "-k", d.service()) }
	}
	if stop == nil {
		stop = func(ctx context.Context) error { return launchctl(ctx, "bootout", d.service()) }
	}
	if listeners == nil {
		listeners = lsofListeners
	}
	if err := restart(ctx); err != nil {
		return fmt.Errorf("the accounts are written, but Decypharr did not restart (start it yourself): %w", err)
	}
	wait := d.Wait
	if wait == 0 {
		wait = 20 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		ls, err := listeners(ctx)
		if err == nil && len(ls) > 0 {
			for _, a := range ls {
				if !loopback(a) {
					_ = stop(ctx)
					return fmt.Errorf("Decypharr listened on %s, not only on 127.0.0.1; it was stopped. Check its config before starting it again", a)
				}
			}
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("the accounts are written and Decypharr restarted, but it is not listening yet; check it")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (d *Decypharr) service() string { return "gui/" + strconv.Itoa(os.Getuid()) + "/" + d.Label }

func launchctl(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return nil
}

// lsofListeners lists the addresses Decypharr listens on (TCP listen and
// unconnected UDP).
func lsofListeners(ctx context.Context) ([]string, error) {
	out, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-a", "-c", "decypharr", "-i").Output()
	if err != nil && len(out) == 0 {
		return nil, nil // lsof exits 1 when nothing matches
	}
	return parseLsof(string(out)), nil
}

func parseLsof(out string) []string {
	var addrs []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 9 || f[0] == "COMMAND" {
			continue
		}
		name := f[len(f)-1]
		proto := f[len(f)-2]
		if name == "(LISTEN)" && len(f) >= 10 {
			addrs = append(addrs, f[len(f)-2])
			continue
		}
		if proto == "UDP" && !strings.Contains(name, "->") {
			addrs = append(addrs, name)
		}
	}
	return addrs
}

func loopback(addr string) bool {
	return strings.HasPrefix(addr, "127.0.0.1:") || strings.HasPrefix(addr, "[::1]:") || strings.HasPrefix(addr, "localhost:")
}
