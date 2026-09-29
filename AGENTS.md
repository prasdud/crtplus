# AGENTS.md

Instructions for agents working in this repository.

## Overview

`crtplus` is a minimal subdomain enumerator for bug bounty recon. It queries the
crt.name certificate-transparency index for an apex domain, normalizes the
results, and resolves which hosts are alive via DNS.

Pure Go standard library. **No external dependencies** — do not add modules
without a strong reason.

## Layout

```
cmd/crtplus/main.go          CLI flags + pipeline wiring + output
internal/crt/crt.go          crt.name client, per-day response cache
internal/crt/validate.go     apex sanity check
internal/resolve/resolve.go  concurrent DNS resolution (worker pool)
internal/browser/browser.go  open URLs in the system default browser
```

## Build

```bash
go build -o crtplus ./cmd/crtplus
```

Or run without building:

```bash
go run ./cmd/crtplus -d example.com
```

## Run

```bash
./crtplus -d <apex> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-d` | — | Apex domain to enumerate (required) |
| `-c` | `50` | Concurrent DNS lookups |
| `-o` | stdout | Write alive hosts to a file |
| `-silent` | `false` | Print hostnames only |
| `-browser` | `false` | Open alive hosts in the default browser |
| `-browser-max` | `0` | Cap tabs opened (0 = all) |
| `-browser-scheme` | `https` | Scheme for opened URLs |

Examples:

```bash
./crtplus -d example.com
./crtplus -d projectdiscovery.io -o alive.txt
./crtplus -d projectdiscovery.io -silent | httpx -silent
./crtplus -d projectdiscovery.io -browser -browser-max 10
```

## Verify changes

Always run these before finishing a change:

```bash
gofmt -l .          # must print nothing
go vet ./...
go build -o /tmp/crtplus ./cmd/crtplus
```

Smoke test (requires network + a spare crt.name request):

```bash
/tmp/crtplus -d example.com -c 30
```

There are no unit tests yet. If you add tests, use `go test ./...`.

## Behavior notes

- **crt.name free tier is 100 requests/IP/day.** Successful responses are cached
  at `~/.recon/cache/crtname/<apex>-<YYYYMMDD>.txt`; same-day reruns read the
  cache and make zero API calls. Never bypass the cache unnecessarily.
- The API returns plain text (one subdomain per line) or HTTP 400 for an
  invalid apex. `ValidateApex` catches obvious bad input before the request.
- Progress, rate-limit, and error messages go to **stderr**; results go to
  **stdout**. Keep that split so `-silent` output stays pipeable.
- A host is "alive" if `net.Resolver` returns an IP or a CNAME.
- `-browser` uses the OS default opener (`xdg-open`/`open`/`rundll32`) via
  fire-and-forget `exec.Command(...).Start()`. Browser failures are non-fatal
  and must never abort a run (the box is often headless).

## Conventions

- Standard library only; keep it dependency-free.
- Follow existing style; run `gofmt`.
- Do not add inline comments unless the logic is non-obvious.
- Bump the tool's behavior in the README when flags change.
