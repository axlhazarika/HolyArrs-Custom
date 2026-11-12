package connections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SABTarget finds the SABnzbd to push to: its address and API key. ok is
// false when no SABnzbd downloader is set up.
type SABTarget func(ctx context.Context) (base, key string, ok bool, err error)

// errNoSAB means no SABnzbd downloader is set up in HolyArrs.
var errNoSAB = errors.New("add SABnzbd under Settings > Downloaders first; servers are pushed to it")

// sab speaks the parts of the SABnzbd config API this module needs. Every
// call is a form POST so the key and passwords stay out of addresses and
// access logs.
type sab struct {
	base string
	key  string
	http *http.Client
}

// sabServer is one entry of get_config section=servers.
type sabServer struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayname"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Enable      int    `json:"enable"`
}

func newSAB(base, key string, h *http.Client) *sab {
	if h == nil {
		h = &http.Client{Timeout: 20 * time.Second}
	}
	return &sab{base: strings.TrimRight(base, "/"), key: key, http: h}
}

func (s *sab) call(ctx context.Context, form url.Values) ([]byte, error) {
	form.Set("apikey", s.key)
	form.Set("output", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/api", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, scrub(err, s.key)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, scrub(err, s.key)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SABnzbd answered %d", resp.StatusCode)
	}
	var e struct {
		Status *bool  `json:"status"`
		Error  string `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		if strings.Contains(strings.ToLower(e.Error), "api key") {
			return nil, errors.New("SABnzbd refused the API key")
		}
		return nil, fmt.Errorf("SABnzbd: %s", e.Error)
	}
	return b, nil
}

// servers lists every server SABnzbd has.
func (s *sab) servers(ctx context.Context) ([]sabServer, error) {
	b, err := s.call(ctx, url.Values{"mode": {"get_config"}, "section": {"servers"}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Config struct {
			Servers []sabServer `json:"servers"`
		} `json:"config"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, errors.New("the address answered, but not like SABnzbd")
	}
	return out.Config.Servers, nil
}

func b01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func serverForm(e Entry) url.Values {
	f := url.Values{
		"host":        {e.Host},
		"port":        {strconv.Itoa(e.Port)},
		"ssl":         {b01(e.SSL)},
		"username":    {e.Username},
		"password":    {e.Secret},
		"connections": {strconv.Itoa(e.Connections)},
	}
	return f
}

// put adds the server or changes it in place. SABnzbd makes a server when
// the keyword is new.
func (s *sab) put(ctx context.Context, e Entry) error {
	f := serverForm(e)
	f.Set("mode", "set_config")
	f.Set("section", "servers")
	f.Set("keyword", sabKeyword(e))
	f.Set("displayname", e.Name)
	f.Set("priority", strconv.Itoa(e.Priority))
	f.Set("enable", b01(e.Enabled))
	_, err := s.call(ctx, f)
	return err
}

// remove deletes the server; a server that is already gone is fine.
func (s *sab) remove(ctx context.Context, e Entry) error {
	_, err := s.call(ctx, url.Values{"mode": {"del_config"}, "section": {"servers"}, "keyword": {sabKeyword(e)}})
	return err
}

// test asks SABnzbd to connect to the server with these details.
func (s *sab) test(ctx context.Context, e Entry) (bool, string, error) {
	f := serverForm(e)
	f.Set("mode", "config")
	f.Set("name", "test_server")
	f.Set("timeout", "10")
	b, err := s.call(ctx, f)
	if err != nil {
		return false, "", err
	}
	var out struct {
		Value struct {
			Result  bool   `json:"result"`
			Message string `json:"message"`
		} `json:"value"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return false, "", errors.New("SABnzbd answered the test in a way this version does not know")
	}
	return out.Value.Result, scrubText(out.Value.Message, e.Secret, s.key), nil
}

// scrub removes secret values from an error's text.
func scrub(err error, values ...string) error {
	if err == nil {
		return nil
	}
	return errors.New(scrubText(err.Error(), values...))
}

func scrubText(s string, values ...string) string {
	for _, v := range values {
		if len(v) >= 4 {
			s = strings.ReplaceAll(s, v, "••••")
			s = strings.ReplaceAll(s, url.QueryEscape(v), "••••")
		}
	}
	return s
}
