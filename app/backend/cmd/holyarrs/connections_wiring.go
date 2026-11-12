package main

import (
	"context"
	"os"

	"github.com/HolyArrs/holyarrs/app/backend/internal/accounts"
	"github.com/HolyArrs/holyarrs/app/backend/internal/connections"
	"github.com/HolyArrs/holyarrs/app/backend/internal/core/webserver"
)

// The Connections routes are the owner's alone. They are listed here, not in
// accounts, so the custom change stays in its own files; the gate treats an
// unlisted route as owner-only anyway.
func init() {
	for _, p := range []string{
		"GET /api/v1/connections",
		"POST /api/v1/connections",
		"PUT /api/v1/connections/{id}",
		"DELETE /api/v1/connections/{id}",
		"POST /api/v1/connections/{id}/test",
		"POST /api/v1/connections/{id}/push",
	} {
		accounts.RoutePerms[p] = webserver.NeedOwner
	}
}

// connectionsService is the Connections settings: Usenet servers pushed to
// SABnzbd and debrid accounts written into Decypharr's config. Decypharr is
// set up only from the environment, never from the browser, so no page can
// point the writer at another file.
func (a *handlers) connectionsService() *connections.Service {
	return &connections.Service{
		Vault: a.s.Secrets,
		SAB:   a.sabTarget,
		Decypharr: &connections.Decypharr{
			ConfigPath: os.Getenv("HOLYARRS_DECYPHARR_CONFIG"),
			Label:      os.Getenv("HOLYARRS_DECYPHARR_LABEL"),
		},
	}
}

// sabTarget is the first SABnzbd downloader in priority order, with its key.
func (a *handlers) sabTarget(ctx context.Context) (string, string, bool, error) {
	if a.dl == nil || a.s.Secrets == nil {
		return "", "", false, nil
	}
	ds, err := a.dl.Manager().Downloaders(ctx)
	if err != nil {
		return "", "", false, err
	}
	for _, d := range ds {
		if d.Adapter != "sabnzbd" {
			continue
		}
		key := ""
		if d.SecretRef != nil {
			if key, err = a.s.Secrets.Get(ctx, *d.SecretRef); err != nil {
				return "", "", false, err
			}
		}
		return d.URL, key, true, nil
	}
	return "", "", false, nil
}
