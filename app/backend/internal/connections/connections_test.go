package connections

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HolyArrs/holyarrs/app/backend/internal/core/database"
	"github.com/HolyArrs/holyarrs/app/backend/internal/core/secrets"
	"github.com/HolyArrs/holyarrs/app/backend/internal/core/webserver"
)

type memVault struct {
	mu sync.Mutex
	m  map[string]string
}

func (v *memVault) Setting(_ context.Context, grp, key string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.m[grp+"/"+key], nil
}

func (v *memVault) SetSetting(_ context.Context, grp, key, plain string, _ database.Actor) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.m == nil {
		v.m = map[string]string{}
	}
	v.m[grp+"/"+key] = plain
	return nil
}

// fakeSAB keeps servers like SABnzbd's config API and records every form.
type fakeSAB struct {
	mu      sync.Mutex
	servers map[string]url.Values
	forms   []url.Values
	queries []string
}

func (f *fakeSAB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, r.URL.RawQuery)
	_ = r.ParseForm()
	f.forms = append(f.forms, r.PostForm)
	if r.PostForm.Get("apikey") != "sabkey" {
		_, _ = w.Write([]byte(`{"status":false,"error":"API Key Incorrect"}`))
		return
	}
	switch r.PostForm.Get("mode") {
	case "set_config":
		k := r.PostForm.Get("keyword")
		if f.servers[k] == nil {
			f.servers[k] = url.Values{}
		}
		for n, v := range r.PostForm {
			f.servers[k][n] = v
		}
		_, _ = w.Write([]byte(`{"config":{}}`))
	case "del_config":
		delete(f.servers, r.PostForm.Get("keyword"))
		_, _ = w.Write([]byte(`{"status":true}`))
	case "get_config":
		var list []map[string]any
		for k, v := range f.servers {
			list = append(list, map[string]any{"name": k, "displayname": v.Get("displayname"), "host": v.Get("host")})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"config": map[string]any{"servers": list}})
	case "config":
		ok := r.PostForm.Get("password") == "right"
		_ = json.NewEncoder(w).Encode(map[string]any{"value": map[string]any{"result": ok, "message": "Connection Successful!"}})
	}
}

const safeConfig = `{
  "session_secret": "s3cr3t-session",
  "bind_address": "127.0.0.1",
  "port": "8282",
  "workers": 4,
  "debrids": [
    {"provider": "torbox", "name": "by-hand", "api_key": "handkey", "download_api_keys": ["handkey"], "workers": 6, "rate_limit": "250/minute"}
  ],
  "hearsay": {"disabled": true, "participate": false, "publish": false},
  "future_key": {"nested": [1, 2.5, "x"]}
}`

type rig struct {
	svc      *Service
	sab      *fakeSAB
	cfg      string
	restarts int
	stops    int
	listen   []string
	late     []string // listeners from the third sample on
	samples  int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{sab: &fakeSAB{servers: map[string]url.Values{"my-own": {"displayname": {"Mine"}, "host": {"news.example"}}}}, listen: []string{"127.0.0.1:8282"}}
	srv := httptest.NewServer(r.sab)
	t.Cleanup(srv.Close)
	r.cfg = filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(r.cfg, []byte(safeConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	r.svc = &Service{
		Vault: &memVault{},
		SAB:   func(context.Context) (string, string, bool, error) { return srv.URL, "sabkey", true, nil },
		Decypharr: &Decypharr{ConfigPath: r.cfg, Label: "test.decypharr", Wait: 300 * time.Millisecond, Settle: 60 * time.Millisecond, Every: 10 * time.Millisecond,
			Restart:   func(context.Context) error { r.restarts++; return nil },
			Stop:      func(context.Context) error { r.stops++; return nil },
			Listeners: func(context.Context) ([]string, error) {
				r.samples++
				if r.late != nil && r.samples >= 3 {
					return r.late, nil
				}
				return r.listen, nil
			}},
	}
	return r
}

func (r *rig) config(t *testing.T) map[string]any {
	t.Helper()
	m, err := readConfig(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func on(b bool) *bool { return &b }

var by = database.Actor{Kind: "user", ID: 1}

func usenet(name, pass string) Input {
	return Input{Kind: KindUsenet, Name: name, Host: "news.provider.example", Port: 563, SSL: true, Username: "u", Secret: pass, Connections: 8, Priority: 0}
}

func TestUsenetServerPushedToSABAndMasked(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	v, err := r.svc.Add(ctx, usenet("Main", "right"), by)
	if err != nil {
		t.Fatal(err)
	}
	if v.Secret != secrets.Masked || !v.HasSecret || v.State != StateOK {
		t.Fatalf("view = %+v", v)
	}
	got := r.sab.servers[sabPrefix+v.ID]
	if got.Get("host") != "news.provider.example" || got.Get("password") != "right" || got.Get("ssl") != "1" || got.Get("enable") != "1" || got.Get("displayname") != "Main" {
		t.Fatalf("SAB got %v", got)
	}
	for _, q := range r.sab.queries {
		if strings.Contains(q, "right") || strings.Contains(q, "sabkey") {
			t.Fatalf("secret in the address: %q", q)
		}
	}
	// A masked secret keeps the stored one.
	in := usenet("Main", secrets.Masked)
	in.Connections = 20
	if _, err := r.svc.Change(ctx, v.ID, in, by); err != nil {
		t.Fatal(err)
	}
	if got := r.sab.servers[sabPrefix+v.ID]; got.Get("password") != "right" || got.Get("connections") != "20" {
		t.Fatalf("after change SAB got %v", got)
	}
	res, err := r.svc.Test(ctx, v.ID)
	if err != nil || !res.OK {
		t.Fatalf("test = %+v, %v", res, err)
	}
	l, err := r.svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(l)
	if strings.Contains(string(b), `"right"`) || strings.Contains(string(b), "pushedKey") {
		t.Fatalf("listing leaks a secret: %s", b)
	}
	if len(l.Unmanaged) != 2 || l.Unmanaged[0].Target != "sabnzbd" || l.Unmanaged[0].Name != "Mine" {
		t.Fatalf("unmanaged = %+v", l.Unmanaged)
	}
	if err := r.svc.Remove(ctx, v.ID, false, by); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.sab.servers[sabPrefix+v.ID]; ok {
		t.Fatal("server still in SAB")
	}
	if _, ok := r.sab.servers["my-own"]; !ok {
		t.Fatal("the server made in SAB was touched")
	}
}

func TestSABDownKeepsEntryPending(t *testing.T) {
	r := newRig(t)
	r.svc.SAB = func(context.Context) (string, string, bool, error) { return "http://127.0.0.1:1", "sabkey", true, nil }
	v, err := r.svc.Add(context.Background(), usenet("Main", "right"), by)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != StatePending || v.LastError == "" {
		t.Fatalf("view = %+v", v)
	}
	if strings.Contains(v.LastError, "right") || strings.Contains(v.LastError, "sabkey") {
		t.Fatalf("error leaks a secret: %s", v.LastError)
	}
	// Removing needs SAB unless the owner keeps it there.
	if err := r.svc.Remove(context.Background(), v.ID, false, by); err == nil {
		t.Fatal("removed although SAB is down")
	}
	if err := r.svc.Remove(context.Background(), v.ID, true, by); err != nil {
		t.Fatal(err)
	}
}

func TestDebridAccountWrittenKeepingEverythingElse(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	v, err := r.svc.Add(ctx, Input{Kind: KindDebrid, Provider: RealDebrid, Name: "realdebrid", Secret: "rdkey1"}, by)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != StateOK || r.restarts != 1 {
		t.Fatalf("view %+v restarts %d", v, r.restarts)
	}
	m := r.config(t)
	if err := checkSafe(m); err != nil {
		t.Fatal(err)
	}
	if m["session_secret"] != "s3cr3t-session" || m["workers"].(json.Number).String() != "4" {
		t.Fatalf("other keys changed: %v", m)
	}
	if fk := m["future_key"].(map[string]any)["nested"].([]any); len(fk) != 3 || fk[1].(json.Number).String() != "2.5" {
		t.Fatalf("unknown key changed: %v", fk)
	}
	ds := debridItems(m)
	if len(ds) != 2 || str(ds[0], "name") != "by-hand" || ds[0]["workers"].(json.Number).String() != "6" || str(ds[1], "api_key") != "rdkey1" {
		t.Fatalf("debrids = %v", ds)
	}
	if fi, _ := os.Stat(r.cfg); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	baks, _ := filepath.Glob(r.cfg + ".holyarrs-*.bak")
	if len(baks) != 1 {
		t.Fatalf("backups %v", baks)
	}

	// New key and a new name: the old item is replaced, not left behind.
	if _, err := r.svc.Change(ctx, v.ID, Input{Name: "rd", Secret: "rdkey2"}, by); err != nil {
		t.Fatal(err)
	}
	ds = debridItems(r.config(t))
	if len(ds) != 2 || str(ds[1], "name") != "rd" || str(ds[1], "api_key") != "rdkey2" {
		t.Fatalf("after change debrids = %v", ds)
	}
	// Unchanged push writes nothing and does not restart.
	if _, err := r.svc.Push(ctx, v.ID, by); err != nil || r.restarts != 2 {
		t.Fatalf("push: %v, restarts %d", err, r.restarts)
	}
	l, _ := r.svc.List(ctx)
	if len(l.Unmanaged) != 2 || l.Unmanaged[1].Name != "by-hand" {
		t.Fatalf("unmanaged %+v", l.Unmanaged)
	}
	// Disabled leaves the config; remove of a disabled one is fine.
	if _, err := r.svc.Change(ctx, v.ID, Input{Name: "rd", Enabled: on(false)}, by); err != nil {
		t.Fatal(err)
	}
	if ds = debridItems(r.config(t)); len(ds) != 1 || str(ds[0], "name") != "by-hand" {
		t.Fatalf("disabled still written: %v", ds)
	}
	if err := r.svc.Remove(ctx, v.ID, false, by); err != nil {
		t.Fatal(err)
	}
	if ds = debridItems(r.config(t)); len(ds) != 1 || str(ds[0], "api_key") != "handkey" {
		t.Fatalf("outside account touched: %v", ds)
	}
}

func TestDebridNameOfAnOutsideAccountIsRefused(t *testing.T) {
	r := newRig(t)
	v, err := r.svc.Add(context.Background(), Input{Kind: KindDebrid, Provider: AllDebrid, Name: "by-hand", Secret: "adkey"}, by)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != StatePending || !strings.Contains(v.LastError, "outside HolyArrs") || r.restarts != 0 {
		t.Fatalf("view %+v restarts %d", v, r.restarts)
	}
	if ds := debridItems(r.config(t)); str(ds[0], "api_key") != "handkey" {
		t.Fatalf("outside account changed: %v", ds)
	}
}

func TestUnsafeDecypharrConfigIsNeverWritten(t *testing.T) {
	for name, cfg := range map[string]string{
		"bind all":        strings.Replace(safeConfig, `"127.0.0.1"`, `""`, 1),
		"bind lan":        strings.Replace(safeConfig, `"127.0.0.1"`, `"0.0.0.0"`, 1),
		"hearsay on":      strings.Replace(safeConfig, `"disabled": true`, `"disabled": false`, 1),
		"participating":   strings.Replace(safeConfig, `"participate": false`, `"participate": true`, 1),
		"publishing":      strings.Replace(safeConfig, `"publish": false`, `"publish": true`, 1),
		"switch renamed":  strings.Replace(safeConfig, `"disabled": true`, `"off": true`, 1),
		"no hearsay part": strings.Replace(safeConfig, `"hearsay"`, `"hearsay_v3"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			if err := os.WriteFile(r.cfg, []byte(cfg), 0o600); err != nil {
				t.Fatal(err)
			}
			v, err := r.svc.Add(context.Background(), Input{Kind: KindDebrid, Provider: RealDebrid, Name: "rd", Secret: "k"}, by)
			if err != nil {
				t.Fatal(err)
			}
			if v.State != StateFailed || r.restarts != 0 {
				t.Fatalf("view %+v restarts %d", v, r.restarts)
			}
			if b, _ := os.ReadFile(r.cfg); string(b) != cfg {
				t.Fatal("config was written")
			}
		})
	}
}

func TestDecypharrListeningBeyondLoopbackIsStopped(t *testing.T) {
	r := newRig(t)
	r.listen = []string{"127.0.0.1:8282", "*:6881"}
	v, err := r.svc.Add(context.Background(), Input{Kind: KindDebrid, Provider: RealDebrid, Name: "rd", Secret: "k"}, by)
	if err != nil {
		t.Fatal(err)
	}
	if r.stops != 1 || v.State != StatePending || !strings.Contains(v.LastError, "stopped") {
		t.Fatalf("stops %d view %+v", r.stops, v)
	}
}

func TestDecypharrNotSetUp(t *testing.T) {
	r := newRig(t)
	r.svc.Decypharr = &Decypharr{}
	v, err := r.svc.Add(context.Background(), Input{Kind: KindDebrid, Provider: RealDebrid, Name: "rd", Secret: "k"}, by)
	if err != nil || v.State != StatePending || !strings.Contains(v.LastError, "HOLYARRS_DECYPHARR_CONFIG") {
		t.Fatalf("view %+v err %v", v, err)
	}
}

func TestParseLsof(t *testing.T) {
	out := `COMMAND     PID USER   FD   TYPE             DEVICE SIZE/OFF NODE NAME
decypharr 123 axl    9u  IPv4 0x1      0t0  TCP 127.0.0.1:8282 (LISTEN)
decypharr 123 axl   10u  IPv4 0x2      0t0  TCP 192.168.1.5:50000->1.2.3.4:443 (ESTABLISHED)
decypharr 123 axl   11u  IPv6 0x3      0t0  UDP *:6881
decypharr 123 axl   12u  IPv4 0x4      0t0  UDP 192.168.1.5:5353->1.1.1.1:53
`
	got := parseLsof(out)
	if len(got) != 2 || got[0] != "127.0.0.1:8282" || got[1] != "*:6881" {
		t.Fatalf("got %v", got)
	}
	if !loopback(got[0]) || loopback(got[1]) {
		t.Fatal("loopback wrong")
	}
}

func TestInputChecks(t *testing.T) {
	bad := []Input{
		{Kind: "nzbget", Name: "x"},
		{Kind: KindUsenet, Name: "", Host: "h", Port: 563, Connections: 1},
		{Kind: KindUsenet, Name: "x", Host: "http://h", Port: 563, Connections: 1},
		{Kind: KindUsenet, Name: "x", Host: "h", Port: 0, Connections: 1},
		{Kind: KindUsenet, Name: "x", Host: "h", Port: 563, Connections: 0},
		{Kind: KindUsenet, Name: "x", Host: "h", Port: 563, Connections: 1, Username: "u"},
		{Kind: KindDebrid, Provider: "torbox", Name: "x", Secret: "k"},
		{Kind: KindDebrid, Provider: RealDebrid, Name: "x"},
		{Kind: KindDebrid, Provider: RealDebrid, Name: "x", Secret: "a\nb"},
	}
	for i, in := range bad {
		var e Entry
		if err := apply(in, &e, true); err == nil {
			t.Errorf("%d: %+v accepted", i, in)
		}
	}
}

func TestDebridCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/rest/1.0/user") {
			_, _ = w.Write([]byte(`{"username":"x","type":"premium","expiration":"2030-01-02T00:00:00.000Z"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"user":{"isPremium":true,"premiumUntil":1893456000}}}`))
	}))
	defer srv.Close()
	s := &Service{RealDebridAPI: srv.URL, AllDebridAPI: srv.URL}
	for _, p := range []string{RealDebrid, AllDebrid} {
		ok, text, err := s.checkDebrid(context.Background(), Entry{Provider: p, Secret: "good"})
		if err != nil || !ok || !strings.HasPrefix(text, "premium until 20") {
			t.Fatalf("%s: %v %q %v", p, ok, text, err)
		}
		ok, _, err = s.checkDebrid(context.Background(), Entry{Provider: p, Secret: "bad"})
		if err != nil || ok {
			t.Fatalf("%s bad key: %v %v", p, ok, err)
		}
	}
}

func TestRoutesNeedAStrongCaller(t *testing.T) {
	r := newRig(t)
	rt := webserver.NewRouter()
	r.svc.Routes(rt)
	for _, rt0 := range rt.Routes() {
		if rt0.Perm != webserver.NeedOwner {
			t.Errorf("%s is %s, want owner", rt0.Pattern, rt0.Perm)
		}
	}
	for _, c := range []struct {
		method, path string
	}{{"GET", "/api/v1/connections"}, {"POST", "/api/v1/connections"}, {"PUT", "/api/v1/connections/x"},
		{"DELETE", "/api/v1/connections/x"}, {"POST", "/api/v1/connections/x/test"}, {"POST", "/api/v1/connections/x/push"}} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(`{}`))
		req = req.WithContext(webserver.ContextWith(req.Context(), webserver.Caller{UserID: 1, Role: "owner", Via: "local_network"}))
		w := httptest.NewRecorder()
		rt.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s by the local network skip: %d", c.method, c.path, w.Code)
		}
	}
	req := httptest.NewRequest("GET", "/api/v1/connections", nil)
	req = req.WithContext(webserver.ContextWith(req.Context(), webserver.Caller{UserID: 1, Role: "owner", Via: "session"}))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("owner session: %d %v", w.Code, w.Header())
	}
}

func TestDebridOutsideItemIsNeverTakenOver(t *testing.T) {
	// Same name and same key as the item made in Decypharr: still refused.
	r := newRig(t)
	v, err := r.svc.Add(context.Background(), Input{Kind: KindDebrid, Provider: RealDebrid, Name: "by-hand", Secret: "handkey"}, by)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != StatePending || !strings.Contains(v.LastError, "outside HolyArrs") || r.restarts != 0 {
		t.Fatalf("view %+v restarts %d", v, r.restarts)
	}
	// Debrid accounts sync together, so drop that one here before the next.
	if err := r.svc.Remove(context.Background(), v.ID, true, by); err != nil {
		t.Fatal(err)
	}
	// Same key under another name: refused too, so no duplicate account.
	v, err = r.svc.Add(context.Background(), Input{Kind: KindDebrid, Provider: RealDebrid, Name: "holyarrs-realdebrid", Secret: "handkey"}, by)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != StatePending || !strings.Contains(v.LastError, "already has this key") || r.restarts != 0 {
		t.Fatalf("view %+v restarts %d", v, r.restarts)
	}
	ds := debridItems(r.config(t))
	if len(ds) != 1 || str(ds[0], "name") != "by-hand" || str(ds[0], "api_key") != "handkey" {
		t.Fatalf("outside account changed: %v", ds)
	}
}

func TestDecypharrLateNonLoopbackListenerIsCaught(t *testing.T) {
	r := newRig(t)
	r.late = []string{"127.0.0.1:8282", "*:4242"}
	v, err := r.svc.Add(context.Background(), Input{Kind: KindDebrid, Provider: RealDebrid, Name: "rd", Secret: "k"}, by)
	if err != nil {
		t.Fatal(err)
	}
	if r.stops != 1 || v.State != StatePending || !strings.Contains(v.LastError, "*:4242") {
		t.Fatalf("stops %d samples %d view %+v", r.stops, r.samples, v)
	}
}

func TestDecypharrThatNeverListensIsStopped(t *testing.T) {
	r := newRig(t)
	r.listen = nil
	v, err := r.svc.Add(context.Background(), Input{Kind: KindDebrid, Provider: RealDebrid, Name: "rd", Secret: "k"}, by)
	if err != nil {
		t.Fatal(err)
	}
	if r.stops != 1 || !strings.Contains(v.LastError, "never started listening") {
		t.Fatalf("stops %d view %+v", r.stops, v)
	}
}

func TestDecypharrNeedsPathAndLabel(t *testing.T) {
	if (&Decypharr{ConfigPath: "/x"}).On() || (&Decypharr{Label: "x"}).On() || !(&Decypharr{ConfigPath: "/x", Label: "x"}).On() {
		t.Fatal("On wrong")
	}
}

func TestListNeverRestartsDecypharr(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.svc.Decypharr.Label = "" // not set up: the account stays pending
	if _, err := r.svc.Add(ctx, Input{Kind: KindDebrid, Provider: RealDebrid, Name: "rd", Secret: "k"}, by); err != nil {
		t.Fatal(err)
	}
	r.svc.Decypharr.Label = "test.decypharr"
	before, _ := os.ReadFile(r.cfg)
	if _, err := r.svc.List(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // a background retry would have run by now
	after, _ := os.ReadFile(r.cfg)
	if r.restarts != 0 || string(before) != string(after) {
		t.Fatalf("a listing wrote or restarted Decypharr (restarts %d)", r.restarts)
	}
}

func TestSABOnlyOnLoopback(t *testing.T) {
	for _, base := range []string{"http://192.168.1.5:8085", "http://sab.example.com:8085", "ftp://127.0.0.1:8085", "http://[::ffff:10.0.0.1]:8085"} {
		if _, err := newSAB(base, "k"); err == nil {
			t.Errorf("%s accepted", base)
		}
	}
	for _, base := range []string{"http://127.0.0.1:8085", "http://localhost:8085/", "http://[::1]:8085"} {
		if _, err := newSAB(base, "k"); err != nil {
			t.Errorf("%s refused: %v", base, err)
		}
	}
	r := newRig(t)
	r.svc.SAB = func(context.Context) (string, string, bool, error) { return "http://10.0.0.2:8085", "sabkey", true, nil }
	v, err := r.svc.Add(context.Background(), usenet("Main", "right"), by)
	if err != nil || v.State != StatePending || !strings.Contains(v.LastError, "this computer") {
		t.Fatalf("view %+v err %v", v, err)
	}
}

func TestSABRedirectNotFollowed(t *testing.T) {
	hit := false
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/api", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c, err := newSAB(srv.URL, "sabkey")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.servers(context.Background()); err == nil || hit {
		t.Fatalf("redirect followed (hit %v, err %v)", hit, err)
	}
}

func TestConfigWrittenWithoutHTMLEscapes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(p, []byte(`{"a":"x<y>&z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeConfig(p, map[string]any{"a": "x<y>&z"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), "x<y>&z") {
		t.Fatalf("escaped: %s", b)
	}
}
