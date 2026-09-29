# AGENTS.md

Instructions for agents working in this repository.

## Overview

`recon-box` is a collection of small, dependency-free Go recon tools for bug
bounty and pentesting. Each tool is a separate binary under `cmd/`; shared code
lives under `internal/`.

Module path: `github.com/prasdud/recon-box`.

Pure Go standard library. **No external dependencies** — do not add modules
without a strong reason.

## Tools

| Tool | Dir | Purpose |
|------|-----|---------|
| `crtplus` | `cmd/crtplus` | Subdomain enumeration via crt.name + DNS liveness + browser open |
| `apix` | `cmd/apix` | API endpoint enumeration: spec discovery, GraphQL introspection, JS mining, Wayback history |
| `bucketrecon` | `cmd/bucketrecon` | S3/R2 bucket recon: name mutation, permission checks, dangling-CNAME takeover |

## Layout

```
cmd/crtplus/main.go                 CLI flags + pipeline wiring + output
cmd/apix/main.go                    CLI flags + wiring + merge/dedup
cmd/bucketrecon/main.go             CLI + orchestration + output
internal/crt/crt.go                 crt.name client, per-day response cache
internal/crt/validate.go            apex sanity check
internal/crt/normalize.go           NormalizeApex: URL/domain input cleanup
internal/resolve/resolve.go         concurrent DNS resolution (worker pool)
internal/browser/browser.go         open URLs in the system default browser
internal/apirecon/apirecon.go       API discovery sources
internal/bucketrecon/bucketrecon.go S3/R2 probing + classification
internal/bucketrecon/mutate.go      candidate bucket-name generation
internal/bucketrecon/passive.go     DNS/JS/Wayback bucket discovery
```

## Build

```bash
go build -o bin/ ./cmd/...            # all tools
go build -o crtplus ./cmd/crtplus     # one tool
go build -o apix    ./cmd/apix
go build -o bucketrecon ./cmd/bucketrecon
```

Or run without building: `go run ./cmd/crtplus -d example.com`.

## Run

### crtplus

```bash
./crtplus -d <apex> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-d` | — | Apex domain or full URL to enumerate (required) |
| `-c` | `50` | Concurrent DNS lookups |
| `-o` | stdout | Write alive hosts to a file |
| `-silent` | `false` | Print hostnames only |
| `-browser` | `false` | Open alive hosts as tabs in a new browser window |
| `-browser-max` | `0` | Cap tabs opened (0 = all) |
| `-browser-scheme` | `https` | Scheme for opened URLs |

```bash
./crtplus -d example.com
./crtplus -d projectdiscovery.io -o alive.txt
./crtplus -d projectdiscovery.io -silent | httpx -silent
./crtplus -d projectdiscovery.io -browser -browser-max 10
```

### apix

```bash
./apix -u <base-api-url> [-js <site-url>] [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-u` | — | Base API URL to enumerate (required) |
| `-js` | — | Site URL whose JS bundles to mine |
| `-t` | `15s` | Per-request timeout |
| `-no-history` | `false` | Skip Wayback passive history |
| `-H` | — | Custom header `Name: value` (repeatable) |
| `-o` | stdout | Write endpoints to a file |
| `-silent` | `false` | Print only endpoints, no progress |

```bash
./apix -u https://api.example.com -js https://app.example.com
./apix -u https://api.example.com -H "Authorization: Bearer $TOKEN"
```

### bucketrecon

```bash
./bucketrecon -d <domain> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-d` | — | Domain to recon (required) |
| `-js` | `https://<domain>` | Site URL to mine for bucket references |
| `-w` | built-in | Extra wordlist file for name mutations |
| `-perm` | `false` | Check anonymous object read on non-listable buckets |
| `-no-brute` | `false` | Skip name permutation (passive only) |
| `-no-dns` | `false` | Skip subdomain/CNAME takeover discovery |
| `-all` | `false` | Include un-referenced brute-force results |
| `-proxy` | — | Proxy URL(s) for S3 probes, comma-separated |
| `-proxy-file` | — | File of proxy URLs (rotated) |
| `-no-canary` | `false` | Skip the S3 sanity canary |
| `-canary-bucket` | `google` | Known-existing bucket for the canary |
| `-t` | `15s` | Per-request timeout |
| `-c` | `30` | Concurrency |
| `-json` | `false` | Output JSON |
| `-o` | stdout | Write results to a file |
| `-silent` | `false` | Print only results, no progress |

```bash
./bucketrecon -d example.com
./bucketrecon -d example.com -no-brute
./bucketrecon -d example.com -perm -json
```

## Verify changes

Always run these before finishing a change:

```bash
gofmt -l .          # must print nothing
go vet ./...
go test ./...
go build -o /tmp/crtplus ./cmd/crtplus
go build -o /tmp/apix ./cmd/apix
go build -o /tmp/bucketrecon ./cmd/bucketrecon
```

Unit tests exist for `internal/bucketrecon` (extraction, mutation, normalization).

Smoke test (crtplus requires network + a spare crt.name request):

```bash
/tmp/crtplus -d example.com -c 30
```

There are no unit tests yet. If you add tests, use `go test ./...`.

## Behavior notes

### crtplus
- Input is normalized by `crt.NormalizeApex`: it accepts a domain or a full URL
  and strips scheme, userinfo, port, path/query/fragment, leading `www.`/`*.`,
  and trailing dots. Reuse it for other tools rather than re-parsing by hand.
- **crt.name free tier is 100 requests/IP/day.** Responses are cached at
  `~/.recon/cache/crtname/<apex>-<YYYYMMDD>.txt`; same-day reruns read the cache
  and make zero API calls. Never bypass the cache unnecessarily.
- Plain text (one subdomain per line) or HTTP 400 for an invalid apex.
- A host is "alive" if `net.Resolver` returns an IP or a CNAME.
- `-browser` opens all alive hosts as tabs in a **single new window** by
  invoking the detected default browser directly with `--new-window <urls...>`
  (the plain OS opener cannot target a new window). Detection: `$BROWSER`, then
  `xdg-settings`/`xdg-mime` → `.desktop` `Exec=`, then known binaries. If none
  found it falls back to the default opener. Fire-and-forget
  `exec.Command(...).Start()`; failures are non-fatal (headless-safe).
- `browser.OpenWindow` returns `(usedNewWindow bool, err error)`.

### apix
- Sources are passive/high-signal by default: OpenAPI/Swagger + GraphQL docs,
  JS bundles, Wayback CDX. There is no wordlist bruteforce yet.
- `apix` sends real HTTP requests to the target. Only run it against targets you
  are authorized to test.
- Endpoint output is tagged by source: `[spec]`, `[graphql]`, `[js]`, `[history]`,
  `[doc]`.

### bucketrecon
- Statuses: `listable` = 200 `ListBucketResult`; `private` = exists, no anonymous
  access (403/redirect, `x-amz-bucket-region` gives region); `available` =
  `NoSuchBucket` (name free); `uncertain` = endpoints disagreed.
- `ProbeS3` cross-checks virtual-hosted + path-style (and regional on redirect),
  retries transport errors, and only asserts `listable`/`private`/`available`
  when consistent. Do not reintroduce a first-endpoint short-circuit.
- By default only target-referenced (`dns`/`js`/`history`) results plus `listable`
  and `[TAKEOVER]` are printed; `-all` shows un-referenced brute noise.
- **A mutated name that exists may belong to an unrelated third party.** Only
  results sourced `dns`/`js`/`history`, or `[TAKEOVER]` (CNAME to an `available`
  bucket), are meaningful.
- R2 public buckets do **not** support root listing; `r2.dev` uses random hashes,
  so R2 is passive-discovery only.
- **Never add write-permission testing** (`PutObject`/`DELETE`). Read/list checks
  only.
- S3 anonymous existence answers can be transient/blanketed; live results depend
  on the source IP. The consensus check reduces false positives but can't fix a
  blanket `NoSuchBucket`.
- **Use HEAD (`HeadBucket`) for S3 probing, not GET.** Some networks rewrite
  GET responses to S3 (returning a blanket `404 NoSuchBucket`) while passing HEAD
  correctly. `probeOnce` HEADs and falls back to GET only on transport error.
  Classification: `200` listable / `403` private / `404` available / `3xx`
  private+region. HEAD has no body, so "listable" is inferred from `200`
  (HeadBucket semantics).
- `Canary` (on by default) HEADs a bucket known to exist (`-canary-bucket`,
  default `google`) and expects `403`/`200`. If it gets `404`, the network is
  masking S3 and results must be reported as unreliable — do not remove.
- `NewClientProxy` supports `http`/`https`/`socks5` proxies with per-request
  rotation; with no proxies it falls back to the default transport (which honors
  `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`).

### both
- Progress, rate-limit, and error messages go to **stderr**; results go to
  **stdout**. Keep that split so `-silent` output stays pipeable.
- All tools except `crtplus`'s crt.name call send live HTTP to the target. Only
  run them against authorized targets.

## Conventions

- Standard library only; keep it dependency-free.
- Follow existing style; run `gofmt`.
- Do not add inline comments unless the logic is non-obvious.
- Keep each tool in its own `cmd/<name>` with shared logic under `internal/`.
- Update the README when flags or behavior change.
