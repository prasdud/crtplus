package apirecon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// Endpoint is a single discovered API route or documentation path.
type Endpoint struct {
	Path   string
	Method string
	Source string
	Status int
}

func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

func get(ctx context.Context, client *http.Client, rawurl string, headers map[string]string) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	applyHeaders(req, headers)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, resp.Header, body, err
}

func applyHeaders(req *http.Request, headers map[string]string) {
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "*/*")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
}

var docPaths = []string{
	"/openapi.json",
	"/openapi.yaml",
	"/openapi",
	"/swagger.json",
	"/swagger.yaml",
	"/swagger/v1/swagger.json",
	"/swagger/v2/swagger.json",
	"/v2/api-docs",
	"/v3/api-docs",
	"/api-docs",
	"/api/docs",
	"/docs",
	"/swagger-ui.html",
	"/swagger-ui/index.html",
	"/swagger/index.html",
	"/swagger/",
	"/redoc",
	"/api/swagger.json",
	"/api/openapi.json",
	"/graphql",
	"/graphiql",
	"/api/graphql",
	"/v1/graphql",
}

// DiscoverDocs probes common documentation paths and extracts endpoints from any
// OpenAPI/Swagger JSON or GraphQL schema it finds.
func DiscoverDocs(ctx context.Context, client *http.Client, base string, headers map[string]string) []Endpoint {
	var (
		mu  sync.Mutex
		out []Endpoint
		wg  sync.WaitGroup
	)
	sem := make(chan struct{}, 8)
	base = strings.TrimRight(base, "/")

	for _, p := range docPaths {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			eps := probeDoc(ctx, client, base, p, headers)
			mu.Lock()
			out = append(out, eps...)
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	return out
}

func probeDoc(ctx context.Context, client *http.Client, base, p string, headers map[string]string) []Endpoint {
	full := base + p
	lower := strings.ToLower(p)

	if strings.Contains(lower, "graphql") || strings.Contains(lower, "graphiql") {
		if eps := graphqlIntrospect(ctx, client, full, headers); len(eps) > 0 {
			return eps
		}
	}

	status, hdr, body, err := get(ctx, client, full, headers)
	if err != nil {
		return nil
	}

	if status == http.StatusOK {
		trimmed := bytes.TrimSpace(body)
		ct := strings.ToLower(hdr.Get("Content-Type"))
		if strings.Contains(ct, "json") || bytes.HasPrefix(trimmed, []byte("{")) {
			if eps := parseOpenAPI(body); len(eps) > 0 {
				return eps
			}
		}
		if bytes.Contains(bytes.ToLower(body), []byte("swagger")) || bytes.Contains(bytes.ToLower(body), []byte("openapi")) {
			return []Endpoint{{Path: p, Source: "doc", Status: status}}
		}
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return []Endpoint{{Path: p, Source: "doc", Status: status}}
	}
	return nil
}

func parseOpenAPI(body []byte) []Endpoint {
	var doc struct {
		Paths    map[string]map[string]json.RawMessage `json:"paths"`
		BasePath string                                `json:"basePath"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || len(doc.Paths) == 0 {
		return nil
	}

	methods := map[string]bool{
		"get": true, "post": true, "put": true, "patch": true, "delete": true, "head": true, "options": true,
	}

	var out []Endpoint
	for p, ops := range doc.Paths {
		path := joinPath(doc.BasePath, p)
		added := false
		for m := range ops {
			lm := strings.ToLower(m)
			if !methods[lm] {
				continue
			}
			out = append(out, Endpoint{Path: path, Method: strings.ToUpper(lm), Source: "spec"})
			added = true
		}
		if !added {
			out = append(out, Endpoint{Path: path, Source: "spec"})
		}
	}
	return out
}

func joinPath(basePath, p string) string {
	if basePath == "" || strings.HasPrefix(p, basePath) {
		return p
	}
	return strings.TrimRight(basePath, "/") + "/" + strings.TrimLeft(p, "/")
}

func graphqlIntrospect(ctx context.Context, client *http.Client, endpoint string, headers map[string]string) []Endpoint {
	query := `{"query":"{__schema{queryType{fields{name}} mutationType{fields{name}}}}"}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(query))
	if err != nil {
		return nil
	}
	applyHeaders(req, headers)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil
	}

	var out struct {
		Data struct {
			Schema struct {
				QueryType struct {
					Fields []struct {
						Name string `json:"name"`
					} `json:"fields"`
				} `json:"queryType"`
				MutationType struct {
					Fields []struct {
						Name string `json:"name"`
					} `json:"fields"`
				} `json:"mutationType"`
			} `json:"__schema"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil
	}

	var eps []Endpoint
	for _, f := range out.Data.Schema.QueryType.Fields {
		if f.Name != "" {
			eps = append(eps, Endpoint{Path: f.Name, Method: "QUERY", Source: "graphql"})
		}
	}
	for _, f := range out.Data.Schema.MutationType.Fields {
		if f.Name != "" {
			eps = append(eps, Endpoint{Path: f.Name, Method: "MUTATION", Source: "graphql"})
		}
	}
	return eps
}

// History pulls previously archived URLs for a host from the Wayback CDX API.
func History(ctx context.Context, client *http.Client, host string) []Endpoint {
	u := "https://web.archive.org/cdx/search/cdx?url=" + url.QueryEscape(host+"/*") +
		"&output=json&fl=original,statuscode&collapse=urlkey&limit=20000"
	_, _, body, err := get(ctx, client, u, nil)
	if err != nil {
		return nil
	}

	var rows [][]string
	if err := json.Unmarshal(body, &rows); err != nil || len(rows) < 2 {
		return nil
	}

	seen := map[string]bool{}
	var out []Endpoint
	for _, row := range rows[1:] {
		if len(row) == 0 {
			continue
		}
		parsed, err := url.Parse(row[0])
		if err != nil {
			continue
		}
		p := parsed.Path
		if p == "" || p == "/" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, Endpoint{Path: p, Source: "history"})
	}
	return out
}

var (
	scriptSrcRe = regexp.MustCompile(`(?i)<script[^>]+src=["']([^"']+)["']`)
	pathRe      = regexp.MustCompile("[\"'`](/[A-Za-z0-9_\\-./{}~%:]+)[\"'`]")
	absoluteRe  = regexp.MustCompile(`https?://[A-Za-z0-9_\-.]+(?::[0-9]+)?(?:/[A-Za-z0-9_\-./{}~%:]*)?`)
	assetExts   = []string{".js", ".mjs", ".css", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp", ".avif", ".woff", ".woff2", ".ttf", ".eot", ".map", ".mp4", ".webm", ".json"}
)

// MineJS fetches a site's HTML and its JavaScript bundles, extracting embedded
// API paths and absolute URLs.
func MineJS(ctx context.Context, client *http.Client, site string, headers map[string]string) []Endpoint {
	base, err := url.Parse(site)
	if err != nil {
		return nil
	}

	_, _, html, err := get(ctx, client, site, headers)
	if err != nil {
		return nil
	}

	seen := map[string]bool{}
	var scripts []string
	for _, m := range scriptSrcRe.FindAllSubmatch(html, -1) {
		src := strings.TrimSpace(string(m[1]))
		if src == "" {
			continue
		}
		ref, err := url.Parse(src)
		if err != nil {
			continue
		}
		abs := base.ResolveReference(ref).String()
		if !seen[abs] {
			seen[abs] = true
			scripts = append(scripts, abs)
		}
	}

	// Extract from inline HTML too.
	extracted := extractPaths(html)

	if len(scripts) > 80 {
		scripts = scripts[:80]
	}

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, 10)
	)
	for _, s := range scripts {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			_, _, js, err := get(ctx, client, s, headers)
			if err != nil {
				return
			}
			found := extractPaths(js)
			mu.Lock()
			extracted = append(extracted, found...)
			mu.Unlock()
		}(s)
	}
	wg.Wait()

	var out []Endpoint
	dedup := map[string]bool{}
	for _, p := range extracted {
		if p == "" || dedup[p] {
			continue
		}
		dedup[p] = true
		out = append(out, Endpoint{Path: p, Source: "js"})
	}
	return out
}

func extractPaths(data []byte) []string {
	var out []string
	for _, m := range pathRe.FindAllSubmatch(data, -1) {
		p := string(m[1])
		if isNoisePath(p) {
			continue
		}
		out = append(out, p)
	}
	for _, m := range absoluteRe.FindAll(data, -1) {
		p := string(m)
		if isNoisePath(p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func isNoisePath(p string) bool {
	if len(p) < 2 || len(p) > 300 {
		return true
	}
	lower := strings.ToLower(p)
	if strings.Contains(lower, "/_next/") || strings.Contains(lower, "node_modules") {
		return true
	}
	for _, ext := range assetExts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	if strings.ContainsAny(p, " \\<>\"'") {
		return true
	}
	return false
}

// SortEndpoints orders endpoints by path then method.
func SortEndpoints(eps []Endpoint) {
	sort.Slice(eps, func(i, j int) bool {
		if eps[i].Path == eps[j].Path {
			return eps[i].Method < eps[j].Method
		}
		return eps[i].Path < eps[j].Path
	})
}

func Describe(e Endpoint) string {
	parts := []string{fmt.Sprintf("%-8s", e.Method), e.Path, fmt.Sprintf("[%s]", e.Source)}
	if e.Status != 0 {
		parts = append(parts, fmt.Sprintf("(%d)", e.Status))
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}
