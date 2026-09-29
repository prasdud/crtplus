package bucketrecon

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type Provider string

const (
	ProviderS3 Provider = "s3"
	ProviderR2 Provider = "r2"
)

type Status string

const (
	StatusListable  Status = "listable"
	StatusPrivate   Status = "private"
	StatusAvailable Status = "available"
	StatusUncertain Status = "uncertain"
	StatusUnknown   Status = "unknown"
)

type Source string

const (
	SourceBrute   Source = "brute"
	SourceDNS     Source = "dns"
	SourceJS      Source = "js"
	SourceHistory Source = "history"
)

// Result is the outcome of probing one bucket or public endpoint.
type Result struct {
	Name     string   `json:"name"`
	Provider Provider `json:"provider"`
	Region   string   `json:"region,omitempty"`
	Status   Status   `json:"status"`
	Source   Source   `json:"source"`
	Endpoint string   `json:"endpoint,omitempty"`
	Listable bool     `json:"listable"`
	Readable bool     `json:"readable"`
	Takeover bool     `json:"takeover,omitempty"`
	Note     string   `json:"note,omitempty"`
}

const ua = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

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

// NewClientProxy returns a client that routes through the given proxies
// (http/https/socks5/socks5h), cycling over the list per request. With no
// proxies it behaves like NewClient.
func NewClientProxy(timeout time.Duration, proxies []string) (*http.Client, error) {
	c := NewClient(timeout)
	if len(proxies) == 0 {
		return c, nil
	}
	urls := make([]*url.URL, 0, len(proxies))
	for _, p := range proxies {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		u, err := url.Parse(p)
		if err != nil {
			return nil, fmt.Errorf("bad proxy %q: %w", p, err)
		}
		urls = append(urls, u)
	}
	if len(urls) == 0 {
		return c, nil
	}

	var mu sync.Mutex
	next := 0
	c.Transport = &http.Transport{
		Proxy: func(*http.Request) (*url.URL, error) {
			mu.Lock()
			defer mu.Unlock()
			u := urls[next%len(urls)]
			next++
			return u, nil
		},
	}
	return c, nil
}

// Canary reports whether S3 is answering honestly, by checking a bucket known
// to exist. If HEAD returns 404 for it, S3 responses are being masked (e.g. a
// network that rewrites bucket lookups) and existence results cannot be trusted.
func Canary(ctx context.Context, client *http.Client, bucket string) error {
	if bucket == "" {
		bucket = "google"
	}
	u := "https://s3.amazonaws.com/" + bucket + "/"
	status, _, _, err := head(ctx, client, u)
	if err != nil {
		status, _, _, err = get(ctx, client, u)
		if err != nil {
			return fmt.Errorf("S3 canary failed to connect: %w", err)
		}
	}
	switch status {
	case http.StatusOK, http.StatusForbidden,
		http.StatusMovedPermanently, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return nil
	}
	return fmt.Errorf("S3 canary bucket %q returned HTTP %d (expected 403/200); S3 responses are unreliable", bucket, status)
}

func do(ctx context.Context, client *http.Client, method, rawurl string) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawurl, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "*/*")
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, resp.Header, body, err
}

func get(ctx context.Context, client *http.Client, rawurl string) (int, http.Header, []byte, error) {
	return do(ctx, client, http.MethodGet, rawurl)
}

func head(ctx context.Context, client *http.Client, rawurl string) (int, http.Header, []byte, error) {
	return do(ctx, client, http.MethodHead, rawurl)
}

type s3Error struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

func s3ErrorCode(body []byte) string {
	if !bytes.Contains(body, []byte("<Code>")) {
		return ""
	}
	var e s3Error
	if err := xml.Unmarshal(body, &e); err != nil {
		return ""
	}
	return e.Code
}

type probeClass int

const (
	classError probeClass = iota
	classListable
	classPrivate
	classAvailable
)

func (c probeClass) status() Status {
	switch c {
	case classListable:
		return StatusListable
	case classPrivate:
		return StatusPrivate
	case classAvailable:
		return StatusAvailable
	default:
		return StatusUnknown
	}
}

// probeOnce classifies a single S3 endpoint using a HEAD (HeadBucket) request.
// HEAD is deliberate: some networks mangle GET responses to S3 (returning a
// blanket 404) while passing HEAD correctly. HeadBucket semantics: 200 = exists
// with ListBucket access, 403 = exists without access, 404 = does not exist.
func probeOnce(ctx context.Context, client *http.Client, rawurl string) (probeClass, string, string) {
	status, hdr, _, err := head(ctx, client, rawurl)
	if err != nil {
		status, hdr, _, err = get(ctx, client, rawurl)
		if err != nil {
			return classError, "", ""
		}
	}
	region := hdr.Get("x-amz-bucket-region")

	switch {
	case status == http.StatusOK:
		return classListable, region, "200"
	case status == http.StatusForbidden:
		return classPrivate, region, "AccessDenied"
	case status == http.StatusMovedPermanently,
		status == http.StatusTemporaryRedirect,
		status == http.StatusPermanentRedirect:
		return classPrivate, region, "redirect"
	case status == http.StatusNotFound:
		return classAvailable, region, "NoSuchBucket"
	case status == http.StatusBadRequest:
		return classError, region, "InvalidBucketName"
	default:
		return classError, region, ""
	}
}

// ProbeS3 checks whether an S3 bucket exists and what anonymous access allows.
// It cross-checks the virtual-hosted and path-style endpoints (and the regional
// endpoint when redirected) and requires them to agree, reporting "uncertain"
// when they do not.
func ProbeS3(ctx context.Context, client *http.Client, name string, checkRead bool) Result {
	res := Result{Name: name, Provider: ProviderS3, Source: SourceBrute, Status: StatusUnknown}

	endpoints := baseEndpoints(name)
	classes, region := crossCheck(ctx, client, &res, endpoints)

	if region != "" && region != "us-east-1" {
		endpoints = append(endpoints, regionalEndpoints(name, region)...)
		classes, region = crossCheck(ctx, client, &res, endpoints)
	}
	res.Region = region

	res.Status = consensus(classes)
	if res.Status == StatusListable {
		res.Listable = true
	}
	if res.Status == StatusUncertain {
		// One tie-break pass to smooth transient S3 answers.
		classes, _ = crossCheck(ctx, client, &res, endpoints)
		res.Status = consensus(classes)
		if res.Status == StatusListable {
			res.Listable = true
		}
	}
	return finalizeRead(ctx, client, res, checkRead)
}

func baseEndpoints(name string) []string {
	var eps []string
	if !strings.Contains(name, ".") {
		eps = append(eps, "https://"+name+".s3.amazonaws.com/")
	}
	return append(eps, "https://s3.amazonaws.com/"+name+"/")
}

func regionalEndpoints(name, region string) []string {
	var eps []string
	if !strings.Contains(name, ".") {
		eps = append(eps, "https://"+name+".s3."+region+".amazonaws.com/")
	}
	return append(eps, "https://s3."+region+".amazonaws.com/"+name+"/")
}

// crossCheck probes every endpoint (retrying only on transport errors) and
// returns the per-endpoint classifications plus the last region seen.
func crossCheck(ctx context.Context, client *http.Client, res *Result, endpoints []string) ([]probeClass, string) {
	var classes []probeClass
	region := ""
	for _, ep := range endpoints {
		var c probeClass = classError
		for attempt := 0; attempt < 2; attempt++ {
			got, r, code := probeOnce(ctx, client, ep)
			if r != "" {
				region = r
			}
			if got != classError {
				c = got
				res.Endpoint = ep
				if code != "" {
					res.Note = code
				}
				break
			}
		}
		classes = append(classes, c)
	}
	return classes, region
}

func consensus(classes []probeClass) Status {
	counts := map[probeClass]int{}
	for _, c := range classes {
		if c != classError {
			counts[c]++
		}
	}
	if counts[classListable] > 0 {
		return StatusListable
	}
	if len(counts) == 0 {
		return StatusUnknown
	}
	if len(counts) == 1 {
		for c := range counts {
			return c.status()
		}
	}
	return StatusUncertain
}

var commonKeys = []string{"index.html", "robots.txt", "crossdomain.xml", "favicon.ico"}

func finalizeRead(ctx context.Context, client *http.Client, res Result, checkRead bool) Result {
	if !checkRead || res.Endpoint == "" {
		return res
	}
	for _, k := range commonKeys {
		u := strings.TrimRight(res.Endpoint, "/") + "/" + k
		if status, _, _, err := head(ctx, client, u); err == nil && status == http.StatusOK {
			res.Readable = true
			res.Note = strings.TrimSpace(res.Note + " readable:" + k)
			break
		}
	}
	return res
}

// ProbeR2 checks a discovered R2 public endpoint. R2 public buckets do not
// support root listing, so "private" is the expected result for a live bucket.
func ProbeR2(ctx context.Context, client *http.Client, name string, endpoint string) Result {
	res := Result{Name: name, Provider: ProviderR2, Source: SourceJS, Endpoint: endpoint}
	status, _, body, err := get(ctx, client, endpoint)
	if err != nil {
		res.Status = StatusUnknown
		res.Note = "unreachable"
		return res
	}
	switch {
	case status == http.StatusOK && bytes.Contains(body, []byte("ListBucketResult")):
		res.Status = StatusListable
		res.Listable = true
	case status == http.StatusOK:
		res.Status = StatusPrivate
		res.Note = "object served"
	case status == http.StatusForbidden || status == http.StatusNotFound:
		res.Status = StatusPrivate
		res.Note = s3ErrorCode(body)
	default:
		res.Status = StatusUnknown
	}
	return res
}

// ProbeAll probes many S3 candidate names concurrently.
func ProbeAll(ctx context.Context, client *http.Client, names []string, concurrency int, checkRead bool) []Result {
	if concurrency < 1 {
		concurrency = 20
	}
	jobs := make(chan string)
	results := make(chan Result)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range jobs {
				results <- ProbeS3(ctx, client, name, checkRead)
			}
		}()
	}
	go func() {
		for _, n := range names {
			jobs <- n
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var out []Result
	for r := range results {
		out = append(out, r)
	}
	return out
}

// Dedup merges results for the same bucket, preferring the most informative.
func Dedup(in []Result) []Result {
	byKey := map[string]Result{}
	order := []string{}
	for _, r := range in {
		key := string(r.Provider) + "|" + r.Name
		prev, ok := byKey[key]
		if !ok {
			byKey[key] = r
			order = append(order, key)
			continue
		}
		byKey[key] = merge(prev, r)
	}
	out := make([]Result, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out
}

func merge(a, b Result) Result {
	m := a
	if rank(b.Status) > rank(a.Status) {
		m.Status = b.Status
		m.Region = b.Region
		m.Endpoint = b.Endpoint
	}
	m.Listable = a.Listable || b.Listable
	m.Readable = a.Readable || b.Readable
	m.Takeover = a.Takeover || b.Takeover
	if m.Region == "" {
		m.Region = b.Region
	}
	if m.Note == "" {
		m.Note = b.Note
	}
	if m.Source == SourceBrute {
		m.Source = b.Source
	}
	return m
}

func rank(s Status) int {
	switch s {
	case StatusListable:
		return 4
	case StatusPrivate:
		return 3
	case StatusUncertain:
		return 2
	case StatusAvailable:
		return 1
	default:
		return 0
	}
}

func SortResults(rs []Result) {
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Provider != rs[j].Provider {
			return rs[i].Provider < rs[j].Provider
		}
		if rs[i].Name != rs[j].Name {
			return rs[i].Name < rs[j].Name
		}
		return rs[i].Source < rs[j].Source
	})
}

func Describe(r Result) string {
	tags := []string{}
	if r.Listable {
		tags = append(tags, "LIST")
	}
	if r.Readable {
		tags = append(tags, "READ")
	}
	if r.Takeover {
		tags = append(tags, "TAKEOVER")
	}
	tag := ""
	if len(tags) > 0 {
		tag = " [" + strings.Join(tags, ",") + "]"
	}
	region := r.Region
	if region == "" {
		region = "-"
	}
	return fmt.Sprintf("%-9s %-45s %-9s %-14s %-7s%s",
		r.Provider, r.Name, region, r.Status, r.Source, tag)
}
