# recon-box

A growing collection of small, dependency-free Go recon tools for bug bounty and pentesting. Each tool is a separate binary under `cmd/`, sharing code under `internal/`.

Pure Go standard library — no external dependencies.

## Tools

| Tool | Command | What it does |
|------|---------|--------------|
| **crtplus** | `cmd/crtplus` | Subdomain enumeration via the [crt.name](https://crt.name) CT index, plus DNS liveness checks and optional browser open |
| **apix** | `cmd/apix` | API endpoint enumeration from OpenAPI/Swagger docs, GraphQL introspection, JavaScript bundles, and Wayback history |
| **bucketrecon** | `cmd/bucketrecon` | S3/Cloudflare R2 bucket reconnaissance: name mutations, existence/permission checks, dangling-CNAME takeover detection |

## Build

```bash
# all tools into ./bin
go build -o bin/ ./cmd/...

# or individually
go build -o crtplus ./cmd/crtplus
go build -o apix    ./cmd/apix
```

Or run from source: `go run ./cmd/crtplus -d example.com`.

---

# crtplus

Give it an apex domain, it pulls everything the crt.name certificate-transparency index has on file and tells you which hosts are alive via DNS.

## Usage

```bash
crtplus -d <apex> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-d` | — | Apex domain **or full URL** to enumerate (**required**) |
| `-c` | `50` | Number of concurrent DNS lookups |
| `-o` | stdout | Write alive hosts to a file |
| `-silent` | `false` | Print only hostnames (no IPs/CNAMEs, no progress logs) |
| `-browser` | `false` | Open every alive host as tabs in a **new** browser window |
| `-browser-max` | `0` | Cap tabs opened; `0` means all |
| `-browser-scheme` | `https` | Scheme for opened URLs (`https` or `http`) |

## Examples

```bash
crtplus -d example.com
crtplus -d https://example.com/      # full URLs are accepted and normalized
crtplus -d projectdiscovery.io -o alive.txt
crtplus -d projectdiscovery.io -silent | httpx -silent
crtplus -d projectdiscovery.io -browser -browser-max 10
```

Rerunning the same day costs **zero** API calls — results are cached locally.

## How it works

Four stages: **enumerate → normalize → resolve → output**.

```
cmd/crtplus/main.go          # CLI flags + pipeline wiring
internal/crt/crt.go          # crt.name client, caching, rate-limit awareness
internal/crt/validate.go     # lightweight apex sanity check
internal/resolve/resolve.go  # concurrent DNS resolution
internal/browser/browser.go  # open URLs in the system default browser
```

1. **Enumerate** — `GET https://crt.name/v1/search?apex=<domain>` returns plain text, one subdomain per line. `ValidateApex` rejects garbage before spending a request. The free tier is **100 requests/IP/day**, so responses are cached at `~/.recon/cache/crtname/<apex>-<YYYYMMDD>.txt`; same-day reruns hit the cache. The `x-ratelimit-remaining` header is printed as your budget drains.
2. **Normalize** — lowercase, strip `*.`/trailing dots, drop the apex and non-subdomains, dedup, sort.
3. **Resolve** — worker pool (`-c`, default 50) using `net.Resolver` with a 5s timeout. A host is **alive** if it resolves to an IP or has a CNAME.
4. **Output** — sorted hosts with IPs (or CNAME target); `-silent` prints hostnames only; `-o` writes to a file. Progress/rate-limit messages go to stderr so stdout stays pipeable.

With `-browser`, alive hosts open as `scheme://<host>` tabs in a **single new window**: the tool detects the default browser (`$BROWSER` → `xdg-settings`/`xdg-mime` → `.desktop` `Exec=` → known binaries) and invokes it with `--new-window <urls...>` (Chromium-family and Firefox). Falls back to the default opener if no window-capable browser is found. Failures are non-fatal and reported to stderr.

---

# apix

Give it a base API URL, it enumerates endpoints from passive, high-signal sources. No guessing by default.

## Usage

```bash
apix -u <base-api-url> [-js <site-url>] [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-u` | — | Base API URL to enumerate (**required**) |
| `-js` | — | Site URL whose JavaScript bundles to mine for paths |
| `-t` | `15s` | Per-request timeout |
| `-no-history` | `false` | Skip Wayback passive history |
| `-H` | — | Custom header `Name: value` (repeatable), e.g. auth |
| `-o` | stdout | Write endpoints to a file |
| `-silent` | `false` | Print only endpoints, no progress |

## Examples

```bash
apix -u https://api.example.com
apix -u https://api.example.com -js https://app.example.com
apix -u https://api.example.com -H "Authorization: Bearer $TOKEN"
```

Output is tagged by source:

```
GET      /v1/verify                       [spec]
POST     /v2/tasks/{task_id}/transitions  [spec]
         /api/v1/users                    [js]
         /v1/legacy-endpoint              [history]
```

## How it works

```
cmd/apix/main.go                # CLI flags + wiring + merge/dedup
internal/apirecon/apirecon.go   # discovery sources
```

1. **Spec/doc discovery** — probes common paths (`/openapi.json`, `/swagger.json`, `/v2/api-docs`, `/api-docs`, `/swagger-ui.html`, …) and parses OpenAPI/Swagger JSON into path+method pairs. A `401`/`403` on a doc path is recorded as an existing-but-guarded endpoint.
2. **GraphQL introspection** — POSTs an introspection query to `/graphql`, `/graphiql`, `/api/graphql` and lists query/mutation field names.
3. **JS mining** — fetches the site HTML, extracts `<script src>`, fetches the bundles, and regexes out root-relative paths and absolute URLs, dropping static assets and framework internals.
4. **Wayback history** — pulls previously archived URLs for the host from the CDX API.

Results are normalized, deduped, and sorted.

---

# bucketrecon

Given a domain, find S3 and Cloudflare R2 buckets associated with it and grade anonymous access. Built for finding **public buckets** and **dangling-CNAME takeover** candidates.

S3 and R2 differ fundamentally: S3 bucket names are global, so name mutation works; R2 names are account-scoped and `r2.dev` URLs use random hashes, so R2 is passive-only — and public R2 buckets don't allow root listing anyway.

## Usage

```bash
bucketrecon -d <domain> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-d` | — | Domain to recon (**required**) |
| `-js` | `https://<domain>` | Site URL to mine for bucket references |
| `-w` | built-in | Extra wordlist file for name mutations |
| `-perm` | `false` | Also check anonymous object read on non-listable buckets |
| `-no-brute` | `false` | Skip name permutation (passive only) |
| `-no-dns` | `false` | Skip subdomain/CNAME takeover discovery |
| `-all` | `false` | Include un-referenced brute-force results (default hides them) |
| `-proxy` | — | Proxy URL(s) for S3 probes, comma-separated (`http`/`https`/`socks5`) |
| `-proxy-file` | — | File of proxy URLs, one per line (rotated) |
| `-no-canary` | `false` | Skip the S3 sanity canary check |
| `-canary-bucket` | `google` | Known-existing bucket used for the canary |
| `-t` | `15s` | Per-request timeout |
| `-c` | `30` | Concurrency |
| `-json` | `false` | Output JSON |
| `-o` | stdout | Write results to a file |
| `-silent` | `false` | Print only results, no progress |

## Examples

```bash
bucketrecon -d example.com                 # full run
bucketrecon -d example.com -no-brute       # passive + CNAME takeover only
bucketrecon -d example.com -perm -json     # include read checks, JSON out
bucketrecon -d example.com -all            # also show un-referenced brute results
```

Output columns: `PROVIDER  BUCKET  REGION  STATUS  SOURCE`, with tags `[LIST]`, `[READ]`, `[TAKEOVER]`. Status is one of `listable` (public listing), `private` (exists, no anonymous access), `available` (name free), `uncertain` (endpoints disagreed), or `unknown`; source is `dns`, `js`, `history`, or `brute`.

By default only **target-referenced** buckets (source `dns`/`js`/`history`) and anything public or `[TAKEOVER]` are shown; un-referenced brute-force noise is hidden unless `-all`.

Before probing, a **canary** HEADs a bucket known to exist (default `google`, override with `-canary-bucket`). If it returns `404`, the network is masking S3 lookups and the tool warns that existence results are unreliable. Probes can optionally be routed through `-proxy`/`-proxy-file` (`http`/`https`/`socks5`, rotated per request).

Probing uses **HEAD** (`HeadBucket`) rather than GET — some networks rewrite GET responses to S3 but pass HEAD. Classification: `200` → `listable`, `403` → `private`, `404` → `available`, `301` → exists + region.

## How it works

```
cmd/bucketrecon/main.go              # CLI + orchestration
internal/bucketrecon/bucketrecon.go  # S3/R2 probing + classification
internal/bucketrecon/mutate.go       # candidate name generation
internal/bucketrecon/passive.go      # DNS-CNAME, JS, Wayback discovery
```

1. **Passive discovery** — subdomains (via `internal/crt`) are resolved and their CNAMEs scanned for S3/R2 endpoints; the site's JS is mined for bucket URLs; Wayback CDX is queried. These are high-signal (tied to the target).
2. **Active enumeration** — bucket names are mutated from the domain (base, base.com, base-com, plus prefix/suffix forms with env/purpose words like `assets`, `uploads`, `backups`, `dev`, `prod`).
3. **Consensus probing** — each candidate is checked against its virtual-hosted and path-style endpoints (and the regional endpoint when redirected), using HEAD (`HeadBucket`). Transport errors are retried, and if the endpoints disagree the result is `uncertain` rather than a guess. `listable` → HEAD `200`; `private` → `403`/redirect; `available` → `404`.
4. **R2** — discovered `pub-*.r2.dev` and `*.r2.cloudflarestorage.com` endpoints are probed directly (no listing expected).
5. **Takeover** — a CNAME pointing at an S3 bucket that returns `NoSuchBucket` is flagged `[TAKEOVER]`.

**Caveat:** a mutated name that exists may belong to an unrelated third party. Only buckets tied to the target (source `dns`/`js`/`history`) or dangling CNAMEs are meaningful. Also note S3 anonymous-existence answers can be transient; the consensus check smooths most of that, but treat single results with caution.

---

## Repository layout

```
cmd/crtplus/                 subdomain enumerator
cmd/apix/                    API endpoint enumerator
cmd/bucketrecon/             S3/R2 bucket recon
internal/crt/                crt.name client, validation, apex normalization
internal/resolve/            DNS worker pool
internal/browser/            system default browser control
internal/apirecon/           API discovery sources
internal/bucketrecon/        S3/R2 probing, mutation, passive discovery
```

## Conventions

Standard library only; keep it dependency-free. Run `gofmt` and `go vet ./...` before finishing. Results go to **stdout**, progress/errors to **stderr**, so `-silent` stays pipeable.

## License

[MIT](LICENSE)
