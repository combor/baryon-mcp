BINARY  := baryon-mcp
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test docker-smoke snapshot clean

build:
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/baryon-mcp

test:
	@unformatted="$$(gofmt -l .)"; if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	bash scripts/render_server_json_test.sh
	go vet ./...
	go test -race ./...

# Builds the container image and drives it through an MCP session with no
# Bridge credentials. -count=1 keeps a cached pass from standing in for a run
# against a freshly built image.
docker-smoke:
	docker build --build-arg VERSION=$(VERSION) -t $(BINARY):smoke .
	BARYON_SMOKE_IMAGE=$(BINARY):smoke go test -count=1 -run TestContainerServesIntrospection ./cmd/baryon-mcp

# Full local release dry-run: binaries, archives, native packages, and MCPB bundles into dist/.
snapshot:
	goreleaser release --snapshot --clean
	scripts/mcpb-pack-all.sh

clean:
	rm -rf dist $(BINARY)
