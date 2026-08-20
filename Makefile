# Supply-chain hardening: never auto-download a newer Go toolchain, and never
# let a build mutate go.mod/go.sum implicitly. See SECURITY.md.
export GOTOOLCHAIN := local
export GOFLAGS := -mod=readonly

# Audit tools are version-pinned; bump deliberately, never @latest.
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.7.0

.PHONY: verify install security-audit

verify:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
	go vet ./...
	go test ./...

install:
	go install ./cmd/archive

security-audit:
	go mod verify
	go list -m -json all | go run ./tools/coolingoff
	GOFLAGS= go run $(GOVULNCHECK) ./...
