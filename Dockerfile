FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o /hostifer-builder .

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y \
    git \
    ca-certificates \
    curl \
    --no-install-recommends && \
    rm -rf /var/lib/apt/lists/*

RUN curl -fsSL https://railpack.com/install.sh | RAILPACK_VERSION=${RAILPACK_VERSION} bash -s -- --yes --bin-dir /usr/local/bin \
    && railpack --version \
    && test -x /usr/local/bin/railpack

COPY --from=builder /hostifer-builder /usr/local/bin/hostifer-builder

ENTRYPOINT ["/usr/local/bin/hostifer-builder"]