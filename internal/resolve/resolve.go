package resolve

import (
	"context"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

type Result struct {
	Host  string
	IPs   []string
	CNAME string
}

// All resolves every host concurrently and returns only the ones that are
// alive (resolve to at least one IP or a CNAME).
func All(ctx context.Context, hosts []string, concurrency int) []Result {
	if concurrency < 1 {
		concurrency = 50
	}

	jobs := make(chan string)
	results := make(chan Result)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for host := range jobs {
				if r, ok := lookup(ctx, host); ok {
					results <- r
				}
			}
		}()
	}

	go func() {
		for _, host := range hosts {
			jobs <- host
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var out []Result
	for r := range results {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

func lookup(ctx context.Context, host string) (Result, bool) {
	res := Result{Host: host}
	r := &net.Resolver{}

	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if cname, err := r.LookupCNAME(lctx, host); err == nil {
		c := strings.TrimSuffix(cname, ".")
		if !strings.EqualFold(c, host) {
			res.CNAME = c
		}
	}

	ips, err := r.LookupHost(lctx, host)
	if err == nil && len(ips) > 0 {
		res.IPs = dedup(ips)
		return res, true
	}
	if res.CNAME != "" {
		return res, true
	}
	return res, false
}

func dedup(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	var out []string
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
