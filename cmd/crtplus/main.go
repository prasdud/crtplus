package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/prasdud/recon-box/internal/browser"
	"github.com/prasdud/recon-box/internal/crt"
	"github.com/prasdud/recon-box/internal/resolve"
)

type options struct {
	domain      string
	concurrency int
	outFile     string
	silent      bool
	open        bool
	openMax     int
	scheme      string
}

func main() {
	var opts options
	flag.StringVar(&opts.domain, "d", "", "apex domain to enumerate (required)")
	flag.IntVar(&opts.concurrency, "c", 50, "concurrent DNS lookups")
	flag.StringVar(&opts.outFile, "o", "", "write alive hosts to a file")
	flag.BoolVar(&opts.silent, "silent", false, "print only hostnames")
	flag.BoolVar(&opts.open, "browser", false, "open alive hosts as tabs in a new browser window")
	flag.IntVar(&opts.openMax, "browser-max", 0, "max tabs to open (0 = all)")
	flag.StringVar(&opts.scheme, "browser-scheme", "https", "scheme for opened URLs (https or http)")
	flag.Parse()

	if opts.domain == "" {
		fmt.Fprintln(os.Stderr, "usage: crtplus -d example.com [-c 50] [-o alive.txt] [-silent] [-browser]")
		flag.PrintDefaults()
		os.Exit(2)
	}

	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(opts options) error {
	ctx := context.Background()
	apex := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(opts.domain)), "www.")

	client := crt.NewClient()
	subs, err := client.Search(ctx, apex)
	if err != nil {
		return err
	}
	if !opts.silent {
		fmt.Fprintf(os.Stderr, "[+] crt.name returned %d subdomains for %s\n", len(subs), apex)
	}

	hosts := normalize(subs, apex)
	if !opts.silent {
		fmt.Fprintf(os.Stderr, "[*] resolving %d unique hosts...\n", len(hosts))
	}

	alive := resolve.All(ctx, hosts, opts.concurrency)
	if !opts.silent {
		fmt.Fprintf(os.Stderr, "[+] %d hosts alive\n", len(alive))
	}

	f := os.Stdout
	if opts.outFile != "" {
		created, err := os.Create(opts.outFile)
		if err != nil {
			return err
		}
		defer created.Close()
		f = created
	}
	w := bufio.NewWriter(f)
	for _, r := range alive {
		switch {
		case opts.silent:
			fmt.Fprintln(w, r.Host)
		case len(r.IPs) > 0:
			fmt.Fprintf(w, "%-50s %s\n", r.Host, strings.Join(r.IPs, ", "))
		default:
			fmt.Fprintf(w, "%-50s CNAME -> %s\n", r.Host, r.CNAME)
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}

	if opts.open {
		openInBrowser(alive, opts)
	}
	return nil
}

func openInBrowser(alive []resolve.Result, opts options) {
	scheme := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(opts.scheme)), "://")
	if scheme == "" {
		scheme = "https"
	}

	limit := len(alive)
	if opts.openMax > 0 && opts.openMax < limit {
		limit = opts.openMax
		if !opts.silent {
			fmt.Fprintf(os.Stderr, "[!] opening first %d of %d hosts\n", limit, len(alive))
		}
	}

	urls := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		urls = append(urls, scheme+"://"+alive[i].Host)
	}

	usedNewWindow, err := browser.OpenWindow(urls)
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "[!] could not open browser: %v\n", err)
	case opts.silent:
	case usedNewWindow:
		fmt.Fprintf(os.Stderr, "[+] opened %d hosts in a new browser window\n", len(urls))
	default:
		fmt.Fprintf(os.Stderr, "[+] opened %d hosts in the default browser\n", len(urls))
	}
}

// normalize lowercases, strips wildcards/dots, drops the apex itself and dedups.
func normalize(subs []string, apex string) []string {
	seen := make(map[string]struct{}, len(subs))
	for _, s := range subs {
		s = strings.ToLower(strings.TrimSpace(s))
		s = strings.TrimPrefix(s, "*.")
		s = strings.TrimSuffix(s, ".")
		if s == "" || s == apex {
			continue
		}
		if !strings.HasSuffix(s, "."+apex) && s != apex {
			continue
		}
		seen[s] = struct{}{}
	}

	hosts := make([]string, 0, len(seen))
	for h := range seen {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	return hosts
}
