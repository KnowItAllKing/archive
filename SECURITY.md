# Security policy

## Supply chain

Dependency handling is intentionally conservative, mirroring the policy used
across sibling repos (7-day cooling-off, no implicit code execution, verified
content). Go's toolchain provides part of this natively; the rest is enforced
by `make security-audit`.

**Native guarantees relied on (do not disable):**

- `go.sum` pins the content hash of every module version; builds fail on
  mismatch. `GONOSUMDB`/`GONOSUMCHECK`/`GOFLAGS=-mod=mod` must not be set.
- Module downloads are verified against the public checksum database
  (`sum.golang.org`) via the default `GOPROXY=proxy.golang.org`.
- Go modules are source-only and run no install scripts.

**Enforced by this repo:**

- **7-day cooling-off** — every module version in the build list must have
  been published at least 7 days before it is depended on.
  `tools/coolingoff` checks publication times against the module proxy and
  fails the audit on any violation. When upgrading dependencies, prefer
  versions that have already aged.
- **No toolchain auto-download** — `GOTOOLCHAIN=local` (exported by the
  Makefile and CI) prevents the `go` command from fetching and executing a
  newer toolchain because a dependency's `go.mod` asked for one.
- **Read-only module graph** — `GOFLAGS=-mod=readonly`: builds never
  implicitly edit `go.mod`/`go.sum`.
- **Pinned audit tooling** — `govulncheck` runs at a version pinned in the
  Makefile, never `@latest`.
- **Pinned CI actions** — GitHub Actions are pinned to full commit SHAs, not
  mutable tags.

## Audit

```sh
make security-audit   # go mod verify + cooling-off check + govulncheck
make verify           # gofmt + vet + tests
```

Both run in CI on every push and pull request.

## Reporting

This is a personal tool. Open a GitHub issue for anything security-relevant.
