set shell := ["bash", "-euo", "pipefail", "-c"]

# The Makefile stays identical to upstream so upstream syncs never conflict on
# it. AXR recipes live here; the rest pass through to make.

dist := "./dist"
exe := if os_family() == "windows" { ".exe" } else { "" }

[private]
default:
    @just --list

# The version comes from AXR_VERSION, not git tags, so axr-v* tags never leak
# into the ocr build's `git describe`.
[doc("Build dist/axr with the version in AXR_VERSION.")]
build-axr:
    @version="$( { cat AXR_VERSION 2>/dev/null || true; } | tr -s '[:space:]' ' ' | sed 's/^ //; s/ $//')"; \
      if [ -z "$version" ]; then echo "AXR_VERSION file is missing or empty" >&2; exit 1; fi; \
      go build -tags axr \
        -ldflags "-X main.Version=$version -X main.GitCommit=$(git rev-parse --short HEAD) -X main.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        -o {{ dist }}/axr{{ exe }} ./cmd/opencodereview

# The package filter matches the Makefile's PACKAGES.
[doc("Vet with the axr tag and run the axr tests.")]
test-axr:
    LC_ALL=C go vet -tags axr $(go list ./... | grep -v /extensions/)
    LC_ALL=C go test -v -race -count=1 -tags axr -run '^TestAXR' ./cmd/opencodereview

# Build the ocr binary.
build:
    make build

# Run the full upstream test suite.
test:
    make test

# Format, tidy, vet and check license headers and English-only sources.
check:
    make check

# Check the 90% coverage threshold.
coverage:
    make coverage

# Remove build output.
clean:
    make clean

# Run any other Makefile target, for example `just make build-all`.
make +targets:
    make {{ targets }}
