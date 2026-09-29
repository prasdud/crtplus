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

## Layout

```
cmd/crtplus/main.go             CLI flags + pipeline wiring + output
cmd/apix/main.go                CLI flags + wiring + merge/dedup
internal/crt/crt.go             crt.name client, per-day response cache
internal/crt/validate.go        apex sanity check
internal/resolve/resolve.go     concurrent DNS resolution (worker pool)
internal/browser/browser.go     open URLs in the system default browser
internal/apirecon/apirecon.go   API discovery sources
```

## Build

```bash
go build -o bin/ ./cmd/...            # all tools
go build -o crtplus ./cmd/crtplus     # one tool
go build -o apix    ./cmd/apix
```

Or run without building: `go run ./cmd/crtplus -d example.com`.

## Run

### crtplus

```bash
./crtplus -d <apex> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-d` | — | Apex domain to enumerate (required) |
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

## Verify changes

Always run these before finishing a change:

```bash
gofmt -l .          # must print nothing
go vet ./...
go build -o /tmp/crtplus ./cmd/crtplus
go build -o /tmp/apix ./cmd/apix
```

Smoke test (crtplus requires network + a spare crt.name request):

```bash
/tmp/crtplus -d example.com -c 30
```

There are no unit tests yet. If you add tests, use `go test ./...`.

## Behavior notes

### crtplus
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

### both
- Progress, rate-limit, and error messages go to **stderr**; results go to
  **stdout**. Keep that split so `-silent` output stays pipeable.

## Conventions

- Standard library only; keep it dependency-free.
- Follow existing style; run `gofmt`.
- Do not add inline comments unless the logic is non-obvious.
- Keep each tool in its own `cmd/<name>` with shared logic under `internal/`.
- Update the README when flags or behavior change.
