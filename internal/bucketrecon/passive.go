package bucketrecon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/prasdud/recon-box/internal/crt"
	"github.com/prasdud/recon-box/internal/resolve"
)

// BucketRef is a bucket (or public endpoint) referenced somewhere.
type BucketRef struct {
	Name     string
	Provider Provider
	Source   Source
	Endpoint string
}

var (
	s3VhostRe = regexp.MustCompile(`(?i)\b([a-z0-9][a-z0-9.\-]*?)\.s3(?:[.\-][a-z0-9\-]+)*\.amazonaws\.com`)
	s3PathRe  = regexp.MustCompile(`(?i)\bs3(?:[.\-][a-z0-9\-]+)*\.amazonaws\.com/([a-z0-9][a-z0-9.\-]*)`)
	r2DevRe   = regexp.MustCompile(`(?i)\b([a-z0-9][a-z0-9.\-]*?)\.r2\.dev`)
	r2StoreRe = regexp.MustCompile(`(?i)\b([a-z0-9]+)\.r2\.cloudflarestorage\.com/([a-z0-9][a-z0-9.\-]*)`)

	scriptSrcRe = regexp.MustCompile(`(?i)<script[^>]+src=["']([^"']+)["']`)
)

// ExtractBuckets finds S3/R2 bucket references in arbitrary text.
func ExtractBuckets(data []byte, source Source) []BucketRef {
	var out []BucketRef
	addS3 := func(name string) {
		if b := normalizeBucket(name); b != "" {
			out = append(out, BucketRef{Name: b, Provider: ProviderS3, Source: source})
		}
	}
	addR2 := func(name string) {
		if name != "" {
			out = append(out, BucketRef{Name: name, Provider: ProviderR2, Source: source})
		}
	}

	for _, m := range s3VhostRe.FindAllSubmatch(data, -1) {
		addS3(string(m[1]))
	}
	for _, m := range s3PathRe.FindAllSubmatch(data, -1) {
		addS3(string(m[1]))
	}
	for _, m := range r2DevRe.FindAllSubmatch(data, -1) {
		addR2(string(m[1]) + ".r2.dev")
	}
	for _, m := range r2StoreRe.FindAllSubmatch(data, -1) {
		account, bucket := string(m[1]), string(m[2])
		out = append(out, BucketRef{
			Name:     bucket,
			Provider: ProviderR2,
			Source:   source,
			Endpoint: "https://" + account + ".r2.cloudflarestorage.com/" + bucket + "/",
		})
	}
	return out
}

// MineJS fetches a site's HTML and JavaScript and extracts bucket references.
func MineJS(ctx context.Context, client *http.Client, site string) []BucketRef {
	base, err := url.Parse(site)
	if err != nil {
		return nil
	}
	_, _, html, err := get(ctx, client, site)
	if err != nil {
		return nil
	}

	refs := ExtractBuckets(html, SourceJS)

	seen := map[string]bool{}
	var scripts []string
	for _, m := range scriptSrcRe.FindAllSubmatch(html, -1) {
		ref, err := url.Parse(strings.TrimSpace(string(m[1])))
		if err != nil {
			continue
		}
		abs := base.ResolveReference(ref).String()
		if !seen[abs] {
			seen[abs] = true
			scripts = append(scripts, abs)
		}
	}
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
			_, _, js, err := get(ctx, client, s)
			if err != nil {
				return
			}
			found := ExtractBuckets(js, SourceJS)
			mu.Lock()
			refs = append(refs, found...)
			mu.Unlock()
		}(s)
	}
	wg.Wait()
	return refs
}

// WaybackRefs pulls archived URLs for a domain and extracts bucket references.
func WaybackRefs(ctx context.Context, client *http.Client, domain string) []BucketRef {
	u := "https://web.archive.org/cdx/search/cdx?url=" + url.QueryEscape(domain+"/*") +
		"&output=json&fl=original&collapse=urlkey&limit=50000"
	_, _, body, err := get(ctx, client, u)
	if err != nil {
		return nil
	}
	var rows [][]string
	if err := json.Unmarshal(body, &rows); err != nil || len(rows) < 2 {
		return nil
	}
	var refs []BucketRef
	for _, row := range rows[1:] {
		if len(row) == 0 {
			continue
		}
		refs = append(refs, ExtractBuckets([]byte(row[0]), SourceHistory)...)
	}
	return refs
}

// DNSRefs enumerates subdomains and returns bucket references found in their
// CNAME targets. This is where dangling-CNAME takeovers surface.
func DNSRefs(ctx context.Context, apex string, concurrency int) []BucketRef {
	subs, err := crt.NewClient().Search(ctx, apex)
	if err != nil {
		return nil
	}
	hosts := normalizeHosts(subs, apex)
	alive := resolve.All(ctx, hosts, concurrency)

	var refs []BucketRef
	for _, r := range alive {
		if r.CNAME == "" {
			continue
		}
		for _, ref := range ExtractBuckets([]byte(r.CNAME), SourceDNS) {
			ref.Endpoint = r.Host + " -> " + r.CNAME
			refs = append(refs, ref)
		}
	}
	return refs
}

func normalizeHosts(subs []string, apex string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range subs {
		s = strings.ToLower(strings.TrimSpace(s))
		s = strings.TrimPrefix(s, "*.")
		s = strings.TrimSuffix(s, ".")
		if s == "" || s == apex || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// RefsToNames returns the unique S3 names from a set of references.
func RefsToNames(refs []BucketRef) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range refs {
		if r.Provider == ProviderS3 && !seen[r.Name] {
			seen[r.Name] = true
			out = append(out, r.Name)
		}
	}
	return out
}
