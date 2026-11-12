package connections

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/HolyArrs/holyarrs/app/backend/internal/core/webserver"
)

// Routes registers the Connections routes. All of them are the owner's
// alone, and every one needs a password sign-in or an API key: the local
// network skip may not read or change these secrets.
func (s *Service) Routes(m *webserver.Router) {
	m.Handle("GET /api/v1/connections", webserver.NeedOwner, http.HandlerFunc(s.list))
	m.Handle("POST /api/v1/connections", webserver.NeedOwner, http.HandlerFunc(s.add))
	m.Handle("PUT /api/v1/connections/{id}", webserver.NeedOwner, http.HandlerFunc(s.change))
	m.Handle("DELETE /api/v1/connections/{id}", webserver.NeedOwner, http.HandlerFunc(s.remove))
	m.Handle("POST /api/v1/connections/{id}/test", webserver.NeedOwner, http.HandlerFunc(s.test))
	m.Handle("POST /api/v1/connections/{id}/push", webserver.NeedOwner, http.HandlerFunc(s.pushOne))
}

const inputHelp = `send {"kind": "usenet" or "debrid", "name": "...", ...}`

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	if !webserver.RequireStrong(w, r) {
		return
	}
	out, err := s.List(r.Context())
	if err != nil {
		webserver.Internal(w, "connections", err)
		return
	}
	webserver.WriteJSONNoStore(w, http.StatusOK, out)
}

func (s *Service) add(w http.ResponseWriter, r *http.Request) {
	if !webserver.RequireStrong(w, r) {
		return
	}
	var in Input
	if !webserver.Decode(w, r, &in, inputHelp) {
		return
	}
	ctx, cancel := pushContext(r)
	defer cancel()
	v, err := s.Add(ctx, in, webserver.RequestActor(r))
	answer(w, http.StatusCreated, v, err)
}

func (s *Service) change(w http.ResponseWriter, r *http.Request) {
	if !webserver.RequireStrong(w, r) {
		return
	}
	var in Input
	if !webserver.Decode(w, r, &in, inputHelp) {
		return
	}
	ctx, cancel := pushContext(r)
	defer cancel()
	v, err := s.Change(ctx, r.PathValue("id"), in, webserver.RequestActor(r))
	answer(w, http.StatusOK, v, err)
}

func (s *Service) remove(w http.ResponseWriter, r *http.Request) {
	if !webserver.RequireStrong(w, r) {
		return
	}
	ctx, cancel := pushContext(r)
	defer cancel()
	keep := r.URL.Query().Get("keepInTarget") == "1"
	err := s.Remove(ctx, r.PathValue("id"), keep, webserver.RequestActor(r))
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	answer(w, 0, nil, err)
}

func (s *Service) test(w http.ResponseWriter, r *http.Request) {
	if !webserver.RequireStrong(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := s.Test(ctx, r.PathValue("id"))
	if err != nil {
		var te *TargetError
		if errors.As(err, &te) {
			webserver.WriteJSONNoStore(w, http.StatusOK, TestResult{OK: false, Text: te.Error()})
			return
		}
	}
	answer(w, http.StatusOK, res, err)
}

func (s *Service) pushOne(w http.ResponseWriter, r *http.Request) {
	if !webserver.RequireStrong(w, r) {
		return
	}
	ctx, cancel := pushContext(r)
	defer cancel()
	v, err := s.Push(ctx, r.PathValue("id"), webserver.RequestActor(r))
	answer(w, http.StatusOK, v, err)
}

// pushContext gives a push time to restart Decypharr and see it listen,
// and lets it finish if the browser goes away.
func pushContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), 90*time.Second)
}

func answer(w http.ResponseWriter, code int, v any, err error) {
	var ie *InputError
	var te *TargetError
	switch {
	case err == nil:
		webserver.WriteJSONNoStore(w, code, v)
	case errors.Is(err, ErrNotFound):
		webserver.FailNoStore(w, http.StatusNotFound, err.Error())
	case errors.As(err, &ie):
		webserver.FailNoStore(w, http.StatusBadRequest, ie.Error())
	case errors.As(err, &te):
		webserver.FailNoStore(w, http.StatusBadGateway, te.Error())
	default:
		webserver.Internal(w, "connections", err)
	}
}
