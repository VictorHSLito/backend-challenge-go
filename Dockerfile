# syntax=docker/dockerfile:1.6
# -----------------------------------------------------------------------
# Stage 1: build
# -----------------------------------------------------------------------
FROM golang:1-alpine AS builder

# The go.mod toolchain directive may pin a Go version newer than the image
# ships. GOTOOLCHAIN=auto lets `go build` download the exact version if
# needed; otherwise it uses the image's own.
ENV GOTOOLCHAIN=auto \
    CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# -----------------------------------------------------------------------
# Stage 2: runtime — distroless static, non-root
# -----------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/api /usr/local/bin/api

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/api"]
