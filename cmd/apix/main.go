package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/prasdud/recon-box/internal/apirecon"
)

type headers []string

func (h *headers) String() string { return strings.Join(*h, ",") }
func (h *headers) Set(v string) error {
	*h = append(*h, v)
	return nil
}

func main() {
	var (
		baseURL   = flag.String("u", "", "base API URL to enumerate (required)")
		jsSite    = flag.String("js", "", "site URL whose JavaScript bundles to mine for paths")
		timeout   = flag.Duration("t", 15*time.Second, "per-request timeout")
		noHistory = flag.Bool("no-history", false, "skip Wayback passive history")
		silent    = flag.Bool("silent", false, "print only endpoints, no progress")
		outFile   = flag.String("o", "", "write endpoints to a file")
		hdr       headers
	)
	flag.Var(&hdr, "H", "custom header 'Name: value' (repeatable)")
	flag.Parse()

	if *baseURL == "" {
		fmt.Fprintln(os.Stderr, "usage: apix -u https://api.example.com [-js https://app.example.com] [-H 'Authorization: Bearer x']")
		flag.PrintDefaults()
		os.Exit(2)
	}

	if err := run(*baseURL, *jsSite, *timeout, *noHistory, *silent, *outFile, hdr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(baseURL, jsSite string, timeout time.Duration, noHistory, silent bool, outFile string, hdr headers) error {
	ctx := context.Background()
	client := apirecon.NewClient(timeout)
	headers := parseHeaders(hdr)

	base := strings.TrimRight(baseURL, "/")
	var all []apirecon.Endpoint

	if !silent {
		fmt.Fprintf(os.Stderr, "[*] probing documentation paths on %s\n", base)
	}
	spec := apirecon.DiscoverDocs(ctx, client, base, headers)
	all = append(all, spec...)
	if !silent {
		fmt.Fprintf(os.Stderr, "[+] spec/doc hits: %d\n", len(spec))
	}

	if u, err := url.Parse(base); err == nil && !noHistory {
		if !silent {
			fmt.Fprintf(os.Stderr, "[*] querying Wayback history for %s\n", u.Host)
		}
		hist := apirecon.History(ctx, client, u.Host)
		all = append(all, hist...)
		if !silent {
			fmt.Fprintf(os.Stderr, "[+] history URLs: %d\n", len(hist))
		}
	}

	if jsSite != "" {
		if !silent {
			fmt.Fprintf(os.Stderr, "[*] mining JavaScript on %s\n", jsSite)
		}
		js := apirecon.MineJS(ctx, client, jsSite, headers)
		all = append(all, js...)
		if !silent {
			fmt.Fprintf(os.Stderr, "[+] JS paths: %d\n", len(js))
		}
	}

	all = dedup(all)
	apirecon.SortEndpoints(all)

	w := bufio.NewWriter(os.Stdout)
	if outFile != "" {
		f, err := os.Create(outFile)
		if err != nil {
			return err
		}
		defer f.Close()
		w = bufio.NewWriter(f)
	}
	for _, e := range all {
		fmt.Fprintln(w, apirecon.Describe(e))
	}
	if err := w.Flush(); err != nil {
		return err
	}

	if !silent {
		fmt.Fprintf(os.Stderr, "[+] %d unique endpoints\n", len(all))
	}
	return nil
}

func parseHeaders(in []string) map[string]string {
	out := map[string]string{}
	for _, h := range in {
		k, v, ok := strings.Cut(h, ":")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

func dedup(in []apirecon.Endpoint) []apirecon.Endpoint {
	seen := map[string]bool{}
	var out []apirecon.Endpoint
	for _, e := range in {
		key := e.Method + "\x00" + e.Path + "\x00" + e.Source
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	return out
}
