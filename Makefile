BINARY  := baryon-mcp
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test snapshot clean

build:
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/baryon-mcp

test:
	@unformatted="$$(gofmt -l .)"; if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	go test -race ./...

# Full local release dry-run: binaries, archives, and MCPB bundles into dist/.
snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf dist $(BINARY)
