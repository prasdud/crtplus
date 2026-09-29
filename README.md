# crtplus

Minimal subdomain enumeration for bug bounty recon. Give it an apex domain, it pulls everything the [crt.name](https://crt.name) certificate-transparency index has on file and tells you which hosts are actually alive via DNS.

Pure Go standard library — no external dependencies, single static binary.

## Part 1 — How to run

### Build

```bash
go build -o crtplus ./cmd/crtplus
```

Or run straight from source:

```bash
go run ./cmd/crtplus -d example.com
```

### Usage

```bash
crtplus -d <apex> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-d` | — | Apex domain to enumerate (**required**) |
| `-c` | `50` | Number of concurrent DNS lookups |
| `-o` | stdout | Write alive hosts to a file |
| `-silent` | `false` | Print only hostnames (no IPs/CNAMEs, no progress logs) |
| `-browser` | `false` | Open every alive host as tabs in a **new** browser window |
| `-browser-max` | `0` | Cap tabs opened; `0` means all |
| `-browser-scheme` | `https` | Scheme for opened URLs (`https` or `http`) |

### Examples

Enumerate and resolve `example.com`:

```bash
crtplus -d example.com
```

```
[+] crt.name returned 3088 subdomains for example.com
[*] resolving 3087 unique hosts...
[+] 1 hosts alive
www.example.com        104.20.23.154, 172.66.147.243
```

Write the alive list to a file:

```bash
crtplus -d projectdiscovery.io -o alive.txt
```

Pipe a clean hostname-only list into other tools:

```bash
crtplus -d projectdiscovery.io -silent | httpx -silent
```

Rerunning the same day costs **zero** API calls — results are cached locally.

Open every alive host as tabs in a **new** browser window:

```bash
crtplus -d projectdiscovery.io -browser
```

Cap it to avoid flooding the browser:

```bash
crtplus -d projectdiscovery.io -browser -browser-max 10
```

## Part 2 — How it works

The tool is a four-stage pipeline: **enumerate → normalize → resolve → output**. Code lives under `internal/`:

```
cmd/crtplus/main.go          # CLI flags + pipeline wiring
internal/crt/crt.go          # crt.name client, caching, rate-limit awareness
internal/crt/validate.go     # lightweight apex sanity check
internal/resolve/resolve.go  # concurrent DNS resolution
internal/browser/browser.go  # open URLs in the system default browser
```

### 1. Enumerate (`internal/crt`)

Queries the free crt.name endpoint:

```
GET https://crt.name/v1/search?apex=<domain>
```

The response is plain text, one subdomain per line. Before spending a request, `ValidateApex` rejects obvious garbage (empty strings, spaces, underscores, URLs, single labels) because the API returns HTTP 400 for anything that isn't an eTLD+1.

The free tier allows **100 requests per IP per day**, so every successful response is cached at:

```
~/.recon/cache/crtname/<apex>-<YYYYMMDD>.txt
```

On any rerun in the same day the cache is read instead of hitting the network. The `x-ratelimit-remaining` response header is printed so you can see your budget draining.

### 2. Normalize

The raw list is cleaned before resolution:

- lowercased and trimmed
- wildcard prefixes (`*.`) and trailing dots stripped
- the apex itself dropped
- entries that aren't subdomains of the apex dropped
- deduplicated, then sorted

### 3. Resolve (`internal/resolve`)

Hosts are pushed through a worker pool (`-c`, default 50 goroutines). Each worker calls `net.Resolver` with a 5-second timeout to look up the CNAME and the host's `A`/`AAAA` records.

A host counts as **alive** if it resolves to at least one IP, or has a CNAME. Dead hosts are silently discarded. Results carry both the IPs and, when the host is an alias, its canonical CNAME target.

### 4. Output (`cmd/crtplus`)

Alive hosts are printed sorted. The default format shows the host plus its IPs (or its CNAME target); `-silent` prints hostnames only. With `-o`, output is written to a file instead of stdout. Progress and rate-limit messages go to stderr, so stdout stays clean for piping.

With `-browser`, the alive hosts are opened as `scheme://<host>`, all as tabs in a **single new browser window**. The OS default opener (`xdg-open`) can only target the existing window, so the tool instead detects the default browser and invokes it directly with `--new-window`:

- Linux: `$BROWSER`, else `xdg-settings`/`xdg-mime` → the `.desktop` file's `Exec=` command, falling back to probing known browsers
- then `--new-window <url1> <url2> …` for Chromium-family and Firefox

If no window-capable browser is found it falls back to the default opener (tabs in the current window) and says so. `-browser-max` caps how many hosts open; failures (e.g. on a headless box) are reported to stderr and never abort the run.

## License

[MIT](LICENSE)
