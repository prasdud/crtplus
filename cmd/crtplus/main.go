package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"crtplus/internal/crt"
	"crtplus/internal/resolve"
)

func main() {
	var (
		domain      = flag.String("d", "", "apex domain to enumerate (required)")
		concurrency = flag.Int("c", 50, "concurrent DNS lookups")
		outFile     = flag.String("o", "", "write alive hosts to a file")
		silent      = flag.Bool("silent", false, "print only hostnames")
	)
	flag.Parse()

	if *domain == "" {
		fmt.Fprintln(os.Stderr, "usage: crtplus -d example.com [-c 50] [-o alive.txt] [-silent]")
		flag.PrintDefaults()
		os.Exit(2)
	}

	if err := run(*domain, *concurrency, *outFile, *silent); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(domain string, concurrency int, outFile string, silent bool) error {
	ctx := context.Background()
	apex := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), "www.")

	client := crt.NewClient()
	subs, err := client.Search(ctx, apex)
	if err != nil {
		return err
	}
	if !silent {
		fmt.Fprintf(os.Stderr, "[+] crt.name returned %d subdomains for %s\n", len(subs), apex)
	}

	hosts := normalize(subs, apex)
	if !silent {
		fmt.Fprintf(os.Stderr, "[*] resolving %d unique hosts...\n", len(hosts))
	}

	alive := resolve.All(ctx, hosts, concurrency)
	if !silent {
		fmt.Fprintf(os.Stderr, "[+] %d hosts alive\n", len(alive))
	}

	var buf bufio.Writer
	if outFile != "" {
		f, err := os.Create(outFile)
		if err != nil {
			return err
		}
		defer f.Close()
		buf = *bufio.NewWriter(f)
	} else {
		buf = *bufio.NewWriter(os.Stdout)
	}
	defer buf.Flush()

	for _, r := range alive {
		switch {
		case silent:
			fmt.Fprintln(&buf, r.Host)
		case len(r.IPs) > 0:
			fmt.Fprintf(&buf, "%-50s %s\n", r.Host, strings.Join(r.IPs, ", "))
		default:
			fmt.Fprintf(&buf, "%-50s CNAME -> %s\n", r.Host, r.CNAME)
		}
	}
	return nil
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
