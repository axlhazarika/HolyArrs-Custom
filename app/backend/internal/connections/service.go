package connections

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/HolyArrs/holyarrs/app/backend/internal/core/database"
)

// ErrNotFound means no entry has that id.
var ErrNotFound = errors.New("no such connection")

// retryEvery is how often a listing retries entries still pending.
const retryEvery = time.Minute

// Service keeps the entries and pushes them to SABnzbd and Decypharr.
type Service struct {
	Vault     Vault
	SAB       SABTarget
	Decypharr *Decypharr
	// HTTP is the client for the debrid providers; nil makes one. SABnzbd
	// always gets its own loopback-only client.
	HTTP *http.Client
	// Provider API addresses for tests; empty means the real ones.
	RealDebridAPI, AllDebridAPI string

	mu        sync.Mutex
	lastRetry time.Time
}

// Unmanaged is an entry in SABnzbd or Decypharr that HolyArrs did not make.
// It is shown, never changed.
type Unmanaged struct {
	Target string `json:"target"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

// Target is the state of one app entries are pushed to.
type Target struct {
	SetUp bool   `json:"setUp"`
	Error string `json:"error,omitempty"`
}

// Listing is what the settings page shows.
type Listing struct {
	Entries   []View      `json:"entries"`
	Unmanaged []Unmanaged `json:"unmanaged"`
	SABnzbd   Target      `json:"sabnzbd"`
	Decypharr Target      `json:"decypharr"`
}

// TestResult is the answer of a connection test.
type TestResult struct {
	OK   bool   `json:"ok"`
	Text string `json:"text"`
	MS   int64  `json:"ms"`
}

// List returns every entry masked, what the targets hold that HolyArrs did
// not make, and whether the targets answer. Pending Usenet servers are
// retried in the background at most once a minute. Debrid accounts never
// are: a page load must not rewrite Decypharr's config or restart it; they
// go out only on a save, a remove or Push now.
func (s *Service) List(ctx context.Context) (Listing, error) {
	s.mu.Lock()
	es, err := load(ctx, s.Vault)
	retry := err == nil && slices.ContainsFunc(es, func(e Entry) bool { return e.Kind == KindUsenet && e.State == StatePending }) &&
		time.Since(s.lastRetry) > retryEvery
	if retry {
		s.lastRetry = time.Now()
	}
	s.mu.Unlock()
	if err != nil {
		return Listing{}, err
	}
	out := Listing{Entries: make([]View, 0, len(es)), Unmanaged: []Unmanaged{}}
	for _, e := range es {
		out.Entries = append(out.Entries, ViewOf(e))
	}
	if c, err := s.sab(ctx); err != nil {
		out.SABnzbd = Target{SetUp: !errors.Is(err, errNoSAB), Error: err.Error()}
	} else {
		out.SABnzbd.SetUp = true
		if servers, err := c.servers(ctx); err != nil {
			out.SABnzbd.Error = err.Error()
		} else {
			for _, x := range servers {
				if !strings.HasPrefix(x.Name, sabPrefix) {
					out.Unmanaged = append(out.Unmanaged, Unmanaged{Target: "sabnzbd", Name: or(x.DisplayName, x.Name), Detail: x.Host})
				}
			}
		}
	}
	out.Decypharr.SetUp = s.Decypharr.On()
	if !out.Decypharr.SetUp {
		out.Decypharr.Error = errNoDecypharr.Error()
	} else if m, err := readConfig(s.Decypharr.ConfigPath); err != nil {
		out.Decypharr.Error = err.Error()
	} else {
		if err := checkSafe(m); err != nil {
			out.Decypharr.Error = err.Error()
		}
		out.Unmanaged = append(out.Unmanaged, unmanagedDebrids(m, es)...)
	}
	if retry {
		go s.retryPending(context.WithoutCancel(ctx))
	}
	return out, nil
}

func (s *Service) sab(ctx context.Context) (*sab, error) {
	if s.SAB == nil {
		return nil, errNoSAB
	}
	base, key, ok, err := s.SAB(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errNoSAB
	}
	return newSAB(base, key)
}

// Add stores a new entry and pushes it. The entry is kept even when the
// push fails; its state says so.
func (s *Service) Add(ctx context.Context, in Input, by database.Actor) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	es, err := load(ctx, s.Vault)
	if err != nil {
		return View{}, err
	}
	e := Entry{ID: newID(), State: StatePending}
	if err := apply(in, &e, true); err != nil {
		return View{}, &InputError{err}
	}
	if slices.ContainsFunc(es, func(x Entry) bool { return x.Kind == e.Kind && strings.EqualFold(x.Name, e.Name) }) {
		return View{}, &InputError{errors.New("name: another entry of this kind already has it")}
	}
	es = append(es, e)
	if err := save(ctx, s.Vault, es, by); err != nil {
		return View{}, err
	}
	es = s.push(ctx, es, e.ID, nil)
	return s.saveAndView(ctx, es, e.ID, by)
}

// Change edits an entry and pushes it.
func (s *Service) Change(ctx context.Context, id string, in Input, by database.Actor) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	es, err := load(ctx, s.Vault)
	if err != nil {
		return View{}, err
	}
	i := slices.IndexFunc(es, func(x Entry) bool { return x.ID == id })
	if i < 0 {
		return View{}, ErrNotFound
	}
	e := es[i]
	if err := apply(in, &e, false); err != nil {
		return View{}, &InputError{err}
	}
	if slices.ContainsFunc(es, func(x Entry) bool { return x.ID != id && x.Kind == e.Kind && strings.EqualFold(x.Name, e.Name) }) {
		return View{}, &InputError{errors.New("name: another entry of this kind already has it")}
	}
	e.State = StatePending
	es[i] = e
	if err := save(ctx, s.Vault, es, by); err != nil {
		return View{}, err
	}
	es = s.push(ctx, es, id, nil)
	return s.saveAndView(ctx, es, id, by)
}

// Push sends one entry to its target again.
func (s *Service) Push(ctx context.Context, id string, by database.Actor) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	es, err := load(ctx, s.Vault)
	if err != nil {
		return View{}, err
	}
	if !slices.ContainsFunc(es, func(x Entry) bool { return x.ID == id }) {
		return View{}, ErrNotFound
	}
	es = s.push(ctx, es, id, nil)
	return s.saveAndView(ctx, es, id, by)
}

// Remove takes the entry out of its target, then out of HolyArrs. When the
// target cannot be reached the entry stays, unless keepInTarget is set:
// then only HolyArrs forgets it and the target's copy shows as unmanaged.
func (s *Service) Remove(ctx context.Context, id string, keepInTarget bool, by database.Actor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	es, err := load(ctx, s.Vault)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(es, func(x Entry) bool { return x.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	e := es[i]
	rest := slices.Delete(slices.Clone(es), i, i+1)
	if !keepInTarget {
		switch e.Kind {
		case KindUsenet:
			c, err := s.sab(ctx)
			if err == nil {
				err = c.remove(ctx, e)
			}
			if err != nil {
				return &TargetError{err}
			}
		case KindDebrid:
			if err := s.Decypharr.Sync(ctx, rest, []Entry{e}); err != nil {
				return &TargetError{err}
			}
		}
	}
	return save(ctx, s.Vault, rest, by)
}

// Test checks one entry: SABnzbd connects to a Usenet server, or the
// provider checks a debrid key.
func (s *Service) Test(ctx context.Context, id string) (TestResult, error) {
	s.mu.Lock()
	es, err := load(ctx, s.Vault)
	s.mu.Unlock()
	if err != nil {
		return TestResult{}, err
	}
	i := slices.IndexFunc(es, func(x Entry) bool { return x.ID == id })
	if i < 0 {
		return TestResult{}, ErrNotFound
	}
	e := es[i]
	start := time.Now()
	var ok bool
	var text string
	if e.Kind == KindUsenet {
		c, cerr := s.sab(ctx)
		if cerr == nil {
			ok, text, err = c.test(ctx, e)
		} else {
			err = cerr
		}
	} else {
		ok, text, err = s.checkDebrid(ctx, e)
	}
	if err != nil {
		return TestResult{}, &TargetError{err}
	}
	if ok && text == "" {
		text = "connected"
	}
	return TestResult{OK: ok, Text: text, MS: time.Since(start).Milliseconds()}, nil
}

// push sends entry id to its target and records the outcome on es. Debrid
// accounts go together, so every debrid entry's state follows the one write.
func (s *Service) push(ctx context.Context, es []Entry, id string, gone []Entry) []Entry {
	i := slices.IndexFunc(es, func(x Entry) bool { return x.ID == id })
	if i < 0 {
		return es
	}
	now := time.Now().UTC()
	switch es[i].Kind {
	case KindUsenet:
		c, err := s.sab(ctx)
		if err == nil {
			err = c.put(ctx, es[i])
		}
		mark(&es[i], err, now)
	case KindDebrid:
		err := s.Decypharr.Sync(ctx, es, gone)
		for j := range es {
			if es[j].Kind != KindDebrid {
				continue
			}
			mark(&es[j], err, now)
			if err == nil {
				if es[j].Enabled {
					es[j].PushedName, es[j].PushedKey = es[j].Name, es[j].Secret
				} else {
					es[j].PushedName, es[j].PushedKey = "", ""
				}
			}
		}
	}
	return es
}

func mark(e *Entry, err error, now time.Time) {
	e.LastPush = now
	switch {
	case err != nil:
		e.State, e.LastError = StatePending, err.Error()
		if errors.Is(err, ErrUnsafeConfig) {
			e.State = StateFailed
		}
	case e.Enabled:
		e.State, e.LastError = StateOK, ""
	default:
		e.State, e.LastError = StateOff, ""
	}
}

func (s *Service) saveAndView(ctx context.Context, es []Entry, id string, by database.Actor) (View, error) {
	if err := save(ctx, s.Vault, es, by); err != nil {
		return View{}, err
	}
	for _, e := range es {
		if e.ID == id {
			return ViewOf(e), nil
		}
	}
	return View{}, ErrNotFound
}

// retryPending pushes Usenet servers still pending to SABnzbd once.
func (s *Service) retryPending(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	es, err := load(ctx, s.Vault)
	if err != nil {
		return
	}
	for _, e := range es {
		if e.Kind == KindUsenet && e.State == StatePending {
			es = s.push(ctx, es, e.ID, nil)
		}
	}
	_ = save(ctx, s.Vault, es, database.System)
}

// InputError is a mistake in what the browser sent.
type InputError struct{ Err error }

func (e *InputError) Error() string { return e.Err.Error() }

// TargetError means SABnzbd, Decypharr or a provider could not do it.
type TargetError struct{ Err error }

func (e *TargetError) Error() string { return e.Err.Error() }
func (e *TargetError) Unwrap() error { return e.Err }
