# syntax=docker/dockerfile:1

# golang:1.26.5-bookworm — keep in step with the toolchain in go.mod.
# Compilation runs on the build machine's own platform and cross-compiles with
# GOARCH, so building the arm64 image needs no emulation.
FROM --platform=$BUILDPLATFORM golang@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599 AS build

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
FROM gcr.io/distroless/static-debian13@sha256:f7f8f729987ad0fdf6b05eeeae94b26e6a0f613bdf46feea7fc40f7bd72953e6

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
