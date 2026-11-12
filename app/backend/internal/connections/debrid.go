package connections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Provider API addresses; tests point them at a fake.
const (
	realDebridAPI = "https://api.real-debrid.com"
	allDebridAPI  = "https://api.alldebrid.com"
)

// checkDebrid asks the provider whether the key works and until when the
// account is premium. It reads nothing else of the account.
func (s *Service) checkDebrid(ctx context.Context, e Entry) (bool, string, error) {
	h := s.HTTP
	if h == nil {
		h = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	var addr string
	switch e.Provider {
	case RealDebrid:
		addr = or(s.RealDebridAPI, realDebridAPI) + "/rest/1.0/user"
	case AllDebrid:
		addr = or(s.AllDebridAPI, allDebridAPI) + "/v4/user?agent=holyarrs"
	default:
		return false, "", errors.New("unknown provider")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Authorization", "Bearer "+e.Secret)
	resp, err := h.Do(req)
	if err != nil {
		return false, "", scrub(err, e.Secret)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return false, "the provider refused the key", nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("the provider answered %d", resp.StatusCode)
	}
	switch e.Provider {
	case RealDebrid:
		var u struct {
			Type       string `json:"type"`
			Expiration string `json:"expiration"`
		}
		if json.Unmarshal(b, &u) != nil {
			return false, "", errors.New("Real-Debrid answered in an unknown way")
		}
		if u.Type != "premium" {
			return false, "the key works, but the account is not premium", nil
		}
		return true, "premium until " + day(u.Expiration), nil
	default:
		var u struct {
			Status string `json:"status"`
			Data   struct {
				User struct {
					IsPremium    bool  `json:"isPremium"`
					PremiumUntil int64 `json:"premiumUntil"`
				} `json:"user"`
			} `json:"data"`
		}
		if json.Unmarshal(b, &u) != nil {
			return false, "", errors.New("AllDebrid answered in an unknown way")
		}
		if u.Status != "success" {
			return false, "the provider refused the key", nil
		}
		if !u.Data.User.IsPremium {
			return false, "the key works, but the account is not premium", nil
		}
		return true, "premium until " + time.Unix(u.Data.User.PremiumUntil, 0).UTC().Format(time.DateOnly), nil
	}
}

func day(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.DateOnly)
	}
	return s
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
