package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/HolyArrs/holyarrs/app/backend/internal/accounts"
	"github.com/HolyArrs/holyarrs/app/backend/internal/core/database"
	"github.com/HolyArrs/holyarrs/app/backend/internal/core/webserver"
	"github.com/HolyArrs/holyarrs/app/backend/internal/mediafiles"
	"github.com/HolyArrs/holyarrs/app/backend/internal/mediaservers"
)

// The Play routes (custom change). Play itself is for anyone who may see
// the Title: it sits under /api/v1/titles/{id}, so the Kid gate answers
// 404 for a Title above a Kid's limit. Asking the server to look for a
// file is for owners and admins; the viewer address is the owner's.
// Their permissions are set here, not in accounts, so the change stays in
// its own files.
func init() {
	accounts.RoutePerms["GET /api/v1/titles/{id}/play"] = webserver.NeedSignedIn
	accounts.RoutePerms["POST /api/v1/titles/{id}/play/scan"] = webserver.NeedUsers
	accounts.RoutePerms["GET /api/v1/media-servers/{id}/play-url"] = webserver.NeedOwner
	accounts.RoutePerms["PUT /api/v1/media-servers/{id}/play-url"] = webserver.NeedOwner
	accounts.ReadRoutes["GET /api/v1/titles/{id}/play"] = true
}

// playRoutes registers the Play routes.
func (a *handlers) playRoutes(m *webserver.Router) {
	if a.media == nil || a.media.Linker == nil {
		return
	}
	l := a.media.Linker
	m.Handle("GET /api/v1/titles/{id}/play", webserver.NeedSignedIn, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		title, item, ok := playIDs(w, r)
		if !ok {
			return
		}
		who, signed := webserver.From(r.Context())
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		ans, err := l.Play(ctx, title, item, !signed || who.Has(webserver.NeedUsers))
		playAnswer(w, ans, err)
	}))
	m.Handle("POST /api/v1/titles/{id}/play/scan", webserver.NeedUsers, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		title, item, ok := playIDs(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		ans, err := l.PlayScan(ctx, title, item)
		playAnswer(w, ans, err)
	}))
	m.Handle("GET /api/v1/media-servers/{id}/play-url", webserver.NeedOwner, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			webserver.Fail(w, http.StatusNotFound, "not found")
			return
		}
		v, err := l.PlayURL(r.Context(), id)
		playURLAnswer(w, v, err)
	}))
	m.Handle("PUT /api/v1/media-servers/{id}/play-url", webserver.NeedOwner, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !webserver.RequireStrong(w, r) {
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			webserver.Fail(w, http.StatusNotFound, "not found")
			return
		}
		var in struct {
			URL string `json:"play_url"`
		}
		if !webserver.Decode(w, r, &in, `send {"play_url": "http://192.168.1.20:8096"} or "" for the server's own address`) {
			return
		}
		v, err := l.SetPlayURL(r.Context(), id, in.URL, webserver.RequestActor(r))
		playURLAnswer(w, v, err)
	}))
}

// playIDs reads the Title id and the optional ?item= episode id.
func playIDs(w http.ResponseWriter, r *http.Request) (int64, int64, bool) {
	title, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		webserver.Fail(w, http.StatusNotFound, "not found")
		return 0, 0, false
	}
	var item int64
	if s := r.URL.Query().Get("item"); s != "" {
		if item, err = strconv.ParseInt(s, 10, 64); err != nil || item <= 0 {
			webserver.Fail(w, http.StatusBadRequest, "item: an episode id")
			return 0, 0, false
		}
	}
	return title, item, true
}

func playAnswer(w http.ResponseWriter, ans mediaservers.PlayAnswer, err error) {
	switch {
	case err == nil:
		webserver.WriteJSONNoStore(w, http.StatusOK, ans)
	case errors.Is(err, mediafiles.ErrNoPlayItem):
		webserver.Fail(w, http.StatusNotFound, "not found")
	default:
		webserver.Internal(w, "play", err)
	}
}

func playURLAnswer(w http.ResponseWriter, v string, err error) {
	switch {
	case err == nil:
		webserver.WriteJSONNoStore(w, http.StatusOK, map[string]string{"play_url": v})
	case errors.Is(err, database.ErrNotFound):
		webserver.Fail(w, http.StatusNotFound, "not found")
	case errors.Is(err, mediaservers.ErrPlayURL):
		webserver.Fail(w, http.StatusBadRequest, err.Error())
	default:
		webserver.Internal(w, "play", err)
	}
}
