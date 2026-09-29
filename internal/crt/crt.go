package crt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const baseURL = "https://crt.name/v1/search"

type Client struct {
	HTTP     *http.Client
	cacheDir string
}

func NewClient() *Client {
	return &Client{
		HTTP:     &http.Client{Timeout: 90 * time.Second},
		cacheDir: defaultCacheDir(),
	}
}

func defaultCacheDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".recon", "cache", "crtname")
	}
	return filepath.Join(os.TempDir(), "crtplus-cache")
}

// Search returns every indexed subdomain for an apex. Results are cached per
// day so repeated runs never burn the free 100 requests/IP/day budget.
func (c *Client) Search(ctx context.Context, apex string) ([]string, error) {
	if err := ValidateApex(apex); err != nil {
		return nil, err
	}

	cachePath := c.cachePath(apex)
	if data, err := os.ReadFile(cachePath); err == nil {
		fmt.Fprintf(os.Stderr, "[+] using cached crt.name results: %s\n", cachePath)
		return parseLines(data), nil
	}

	subs, remaining, err := c.fetch(ctx, apex)
	if err != nil {
		return nil, err
	}
	if remaining != "" {
		fmt.Fprintf(os.Stderr, "[+] crt.name rate limit remaining: %s\n", remaining)
	}

	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
		_ = os.WriteFile(cachePath, []byte(strings.Join(subs, "\n")+"\n"), 0o644)
	}
	return subs, nil
}

func (c *Client) fetch(ctx context.Context, apex string) ([]string, string, error) {
	u := baseURL + "?apex=" + url.QueryEscape(apex)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	remaining := resp.Header.Get("x-ratelimit-remaining")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, remaining, err
	}

	if resp.StatusCode == http.StatusBadRequest {
		return nil, remaining, fmt.Errorf("invalid apex %q: %s", apex, strings.TrimSpace(string(body)))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, remaining, fmt.Errorf("crt.name returned HTTP %d", resp.StatusCode)
	}
	return parseLines(body), remaining, nil
}

func (c *Client) cachePath(apex string) string {
	return filepath.Join(c.cacheDir, apex+"-"+time.Now().Format("20060102")+".txt")
}

func parseLines(data []byte) []string {
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
