package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/prasdud/recon-box/internal/bucketrecon"
)

func main() {
	var (
		domain   = flag.String("d", "", "domain to recon buckets for (required)")
		jsSite   = flag.String("js", "", "site URL to mine for bucket references (default https://<domain>)")
		wordlist = flag.String("w", "", "extra wordlist file for bucket-name mutations")
		perm     = flag.Bool("perm", false, "also check anonymous object read on non-listable buckets")
		noBrute  = flag.Bool("no-brute", false, "skip bucket-name permutation")
		noDNS    = flag.Bool("no-dns", false, "skip subdomain/CNAME takeover discovery")
		all      = flag.Bool("all", false, "include un-referenced brute-force results (default: only target-referenced + public)")
		proxy    = flag.String("proxy", "", "proxy URL(s) for S3 probes, comma-separated (http/https/socks5)")
		proxyFil = flag.String("proxy-file", "", "file of proxy URLs, one per line")
		noCanary = flag.Bool("no-canary", false, "skip the S3 sanity canary check")
		canaryBk = flag.String("canary-bucket", "google", "known-existing bucket for the S3 sanity canary")
		timeout  = flag.Duration("t", 15*time.Second, "per-request timeout")
		conc     = flag.Int("c", 30, "concurrency")
		outFile  = flag.String("o", "", "write results to a file")
		silent   = flag.Bool("silent", false, "print only results, no progress")
		asJSON   = flag.Bool("json", false, "output JSON")
	)
	flag.Parse()

	if *domain == "" {
		fmt.Fprintln(os.Stderr, "usage: bucketrecon -d example.com [-js https://example.com] [-perm] [-no-brute] [-no-dns]")
		flag.PrintDefaults()
		os.Exit(2)
	}

	opts := options{
		domain:   *domain,
		jsSite:   *jsSite,
		wordlist: *wordlist,
		perm:     *perm,
		noBrute:  *noBrute,
		noDNS:    *noDNS,
		all:      *all,
		proxy:    *proxy,
		proxyFil: *proxyFil,
		canary:   !*noCanary,
		canaryBk: *canaryBk,
		timeout:  *timeout,
		conc:     *conc,
		outFile:  *outFile,
		silent:   *silent,
		asJSON:   *asJSON,
	}
	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type options struct {
	domain   string
	jsSite   string
	wordlist string
	perm     bool
	noBrute  bool
	noDNS    bool
	all      bool
	proxy    string
	proxyFil string
	canary   bool
	canaryBk string
	timeout  time.Duration
	conc     int
	outFile  string
	silent   bool
	asJSON   bool
}

func run(opts options) error {
	ctx := context.Background()

	proxies := []string{}
	if opts.proxy != "" {
		proxies = append(proxies, strings.Split(opts.proxy, ",")...)
	}
	if opts.proxyFil != "" {
		fileProxies, err := bucketrecon.LoadWords(opts.proxyFil)
		if err != nil {
			return err
		}
		proxies = append(proxies, fileProxies...)
	}
	client, err := bucketrecon.NewClientProxy(opts.timeout, proxies)
	if err != nil {
		return err
	}

	if opts.canary {
		if err := bucketrecon.Canary(ctx, client, opts.canaryBk); err != nil {
			fmt.Fprintf(os.Stderr, "[!] %v\n", err)
			fmt.Fprintln(os.Stderr, "[!] treat bucket existence results as UNRELIABLE — run from a clean network")
		} else if !opts.silent {
			fmt.Fprintln(os.Stderr, "[+] S3 canary OK")
		}
	}

	domain := strings.TrimPrefix(strings.TrimPrefix(opts.domain, "https://"), "http://")
	domain = strings.TrimSuffix(domain, "/")
	apex := strings.TrimPrefix(strings.ToLower(domain), "www.")

	var refs []bucketrecon.BucketRef

	if !opts.noDNS {
		if !opts.silent {
			fmt.Fprintf(os.Stderr, "[*] enumerating subdomains/CNAMEs for %s\n", apex)
		}
		dnsRefs := bucketrecon.DNSRefs(ctx, apex, opts.conc)
		refs = append(refs, dnsRefs...)
		if !opts.silent {
			fmt.Fprintf(os.Stderr, "[+] DNS bucket refs: %d\n", len(dnsRefs))
		}
	}

	site := opts.jsSite
	if site == "" {
		site = "https://" + domain
	}
	if !opts.silent {
		fmt.Fprintf(os.Stderr, "[*] mining %s for bucket references\n", site)
	}
	jsRefs := bucketrecon.MineJS(ctx, client, site)
	refs = append(refs, jsRefs...)
	if !opts.silent {
		fmt.Fprintf(os.Stderr, "[+] JS bucket refs: %d\n", len(jsRefs))
	}

	if !opts.silent {
		fmt.Fprintf(os.Stderr, "[*] querying Wayback for %s\n", apex)
	}
	histRefs := bucketrecon.WaybackRefs(ctx, client, apex)
	refs = append(refs, histRefs...)
	if !opts.silent {
		fmt.Fprintf(os.Stderr, "[+] history bucket refs: %d\n", len(histRefs))
	}

	sourceOf := map[string]bucketrecon.Source{}
	for _, r := range refs {
		name := r.Name
		if r.Provider == bucketrecon.ProviderR2 {
			name = r.Endpoint
			if name == "" {
				name = r.Name
			}
		}
		if prev, ok := sourceOf[name]; !ok || sourceRank(r.Source) > sourceRank(prev) {
			sourceOf[name] = r.Source
		}
	}
	dnsNames := map[string]bool{}
	for _, r := range refs {
		if r.Source == bucketrecon.SourceDNS && r.Provider == bucketrecon.ProviderS3 {
			dnsNames[r.Name] = true
		}
	}

	var results []bucketrecon.Result

	if !opts.noBrute {
		words, err := loadWords(opts.wordlist)
		if err != nil {
			return err
		}
		candidates := bucketrecon.Candidates(domain, words)
		candidates = append(candidates, bucketrecon.RefsToNames(refs)...)
		if !opts.silent {
			fmt.Fprintf(os.Stderr, "[*] probing %d S3 bucket candidates\n", len(candidates))
		}
		results = append(results, bucketrecon.ProbeAll(ctx, client, candidates, opts.conc, opts.perm)...)
	} else {
		for _, name := range bucketrecon.RefsToNames(refs) {
			results = append(results, bucketrecon.ProbeS3(ctx, client, name, opts.perm))
		}
	}

	for i := range results {
		if s, ok := sourceOf[results[i].Name]; ok && sourceRank(s) > sourceRank(results[i].Source) {
			results[i].Source = s
		}
		if dnsNames[results[i].Name] && results[i].Status == bucketrecon.StatusAvailable {
			results[i].Takeover = true
			results[i].Source = bucketrecon.SourceDNS
		}
	}

	for _, r := range refs {
		if r.Provider != bucketrecon.ProviderR2 {
			continue
		}
		endpoint := r.Endpoint
		if endpoint == "" && strings.HasSuffix(r.Name, ".r2.dev") {
			endpoint = "https://" + r.Name + "/"
		}
		if endpoint == "" {
			continue
		}
		results = append(results, bucketrecon.ProbeR2(ctx, client, r.Name, endpoint))
	}

	results = bucketrecon.Dedup(results)
	bucketrecon.SortResults(results)

	if !opts.all {
		kept := results[:0]
		hidden := 0
		for _, r := range results {
			if shownByDefault(r) {
				kept = append(kept, r)
			} else {
				hidden++
			}
		}
		results = kept
		if hidden > 0 && !opts.silent {
			fmt.Fprintf(os.Stderr, "[i] hidden %d un-referenced brute results (-all to show)\n", hidden)
		}
	}

	return writeResults(results, opts)
}

// shownByDefault keeps target-referenced buckets and anything public or a
// takeover candidate; un-referenced brute-force noise is hidden unless -all.
func shownByDefault(r bucketrecon.Result) bool {
	if r.Takeover || r.Listable {
		return true
	}
	switch r.Source {
	case bucketrecon.SourceDNS, bucketrecon.SourceJS, bucketrecon.SourceHistory:
		return true
	}
	return false
}

func loadWords(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	return bucketrecon.LoadWords(path)
}

func writeResults(results []bucketrecon.Result, opts options) error {
	var w *bufio.Writer
	if opts.outFile != "" {
		f, err := os.Create(opts.outFile)
		if err != nil {
			return err
		}
		defer f.Close()
		w = bufio.NewWriter(f)
	} else {
		w = bufio.NewWriter(os.Stdout)
	}

	if opts.asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			return err
		}
	} else {
		if !opts.silent {
			fmt.Fprintln(w, "PROVIDER  BUCKET                                        REGION    STATUS         SOURCE")
		}
		for _, r := range results {
			fmt.Fprintln(w, bucketrecon.Describe(r))
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}

	if !opts.silent {
		var listable, private, uncertain, takeover int
		for _, r := range results {
			switch r.Status {
			case bucketrecon.StatusListable:
				listable++
			case bucketrecon.StatusPrivate:
				private++
			case bucketrecon.StatusUncertain:
				uncertain++
			}
			if r.Takeover {
				takeover++
			}
		}
		fmt.Fprintf(os.Stderr, "[+] %d results: %d listable, %d private, %d uncertain, %d takeover candidates\n",
			len(results), listable, private, uncertain, takeover)
	}
	return nil
}

func sourceRank(s bucketrecon.Source) int {
	switch s {
	case bucketrecon.SourceDNS:
		return 4
	case bucketrecon.SourceJS:
		return 3
	case bucketrecon.SourceHistory:
		return 2
	default:
		return 1
	}
}
