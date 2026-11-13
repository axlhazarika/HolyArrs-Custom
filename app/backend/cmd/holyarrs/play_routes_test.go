package main

import (
	"fmt"
	"strings"
	"testing"
)

// Play is for whoever may see the Title: a Kid gets 404 above their limit,
// the same answer as for a Title that is not there.
func TestPlayRouteFollowsKidLimits(t *testing.T) {
	t.Parallel()
	k := newKidRig(t)
	for _, id := range blockedIDs[:4] {
		if code, body := k.kai.get(fmt.Sprintf("/api/v1/titles/%d/play", id)); code != 404 || strings.TrimSpace(body) != `{"error":"not found"}` {
			t.Errorf("kid, title %d: %d %s", id, code, body)
		}
	}
	// Within the limit Play answers, here with why there is no link yet.
	body := string(k.kai.call("GET", fmt.Sprintf("/api/v1/titles/%d/play", k.ids["Paper Kite"]), "", 200))
	if !strings.Contains(body, `"why"`) || strings.Contains(body, "can_scan") {
		t.Errorf("kid, allowed title: %s", body)
	}
	k.ana.call("GET", fmt.Sprintf("/api/v1/titles/%d/play", k.ids["Night Courier"]), "", 200)
	// A show needs one of its own episodes.
	k.owner.call("GET", fmt.Sprintf("/api/v1/titles/%d/play", k.ids["Harbor Friends"]), "", 404)
	k.owner.call("GET", fmt.Sprintf("/api/v1/titles/%d/play?item=abc", k.ids["Paper Kite"]), "", 400)
}

// Asking the server to look for a file is for owners and admins only, and
// the viewer address is the owner's.
func TestPlayScanAndViewerAddressNeedTheOwner(t *testing.T) {
	t.Parallel()
	k := newKidRig(t)
	scan := fmt.Sprintf("/api/v1/titles/%d/play/scan", k.ids["Paper Kite"])
	k.ana.call("POST", scan, "", 403)
	k.kai.call("POST", scan, "", 403)
	k.owner.call("POST", scan, "", 200)
	k.ana.call("GET", "/api/v1/media-servers/1/play-url", "", 403)
	k.ana.call("PUT", "/api/v1/media-servers/1/play-url", `{"play_url":"http://x:1"}`, 403)
	k.owner.call("GET", "/api/v1/media-servers/999/play-url", "", 404)
	k.owner.call("PUT", "/api/v1/media-servers/999/play-url", `{"play_url":"http://x:1"}`, 404)
}
