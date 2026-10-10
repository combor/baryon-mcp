# syntax=docker/dockerfile:1

# golang:1.27.2-trixie, keep in step with the toolchain in go.mod.
# Compilation runs on the build machine's own platform and cross-compiles with
# GOARCH, so building the arm64 image needs no emulation.
FROM --platform=$BUILDPLATFORM golang@sha256:e58d6f83b3416618d8bcac2b3dde1b7f7e3c4a77d25e88637f8bbae81536c48d AS build

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
FROM gcr.io/distroless/static-debian13@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3

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
