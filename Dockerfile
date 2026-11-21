# syntax=docker/dockerfile:1

# golang:1.27.4-trixie, keep in step with the toolchain in go.mod.
# Compilation runs on the build machine's own platform and cross-compiles with
# GOARCH, so building the arm64 image needs no emulation.
FROM --platform=$BUILDPLATFORM golang@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /baryon-mcp ./cmd/baryon-mcp

# gcr.io/distroless/static-debian13:nonroot — no shell, no package manager.
FROM gcr.io/distroless/static-debian13@sha256:1a64f46364620465ef9e9a8f5413757dfdf22695e050bbfd7303eeaf17a59383

LABEL org.opencontainers.image.title="baryon-mcp" \
      org.opencontainers.image.description="Read Proton Mail and save drafts through your local Proton Mail Bridge." \
      org.opencontainers.image.source="https://github.com/combor/baryon-mcp" \
      org.opencontainers.image.licenses="BSD-3-Clause"

COPY --from=build /baryon-mcp /baryon-mcp

# The image carries no credentials, so this lets a directory or client start it
# and read the tool schemas. Supplying both Bridge credentials at run time
# switches the server to a real mailbox; supplying one of them fails startup.
ENV BARYON_ALLOW_UNCONFIGURED_INTROSPECTION=true

USER 65532:65532
ENTRYPOINT ["/baryon-mcp"]
