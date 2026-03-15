# ─── Stage 1: Build the Go binary ────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

WORKDIR /src

# Download dependencies first so this layer is cached unless go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
RUN go build -o /hostifer-builder .

# ─── Stage 2: Minimal runtime image ──────────────────────────────────────────
FROM alpine:3.19

# git is required by git.go (shelled out via os/exec).
RUN apk add --no-cache bash ca-certificates curl git tar

# Download the Railpack CLI for linux/amd64.
RUN curl -fsSL https://railpack.com/install.sh | bash -s -- --yes --bin-dir /usr/local/bin \
    && test -x /usr/local/bin/railpack

# Copy the compiled builder binary from Stage 1.
COPY --from=builder /hostifer-builder /hostifer-builder

ENTRYPOINT ["/hostifer-builder"]
