# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /src

# Copy both module files so the build resolves to the exact pinned versions
# rather than re-resolving and possibly drifting.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# -mod=readonly fails the build if the source needs a dependency that is not
# already in go.mod, instead of silently adding one.
RUN CGO_ENABLED=0 GOOS=linux go build -mod=readonly -trimpath \
        -ldflags="-s -w" -o /out/server ./cmd/server

# Typst renders the Dashhold-EdTech PDF booklets. The static musl build is
# pinned by version and SHA-256 and run once, so a bad download fails the build
# rather than the first export.
FROM alpine:3.20 AS typst
ARG TYPST_VERSION=0.15.1
ARG TYPST_SHA256=a6d077d0a95eed5a2eba715b2dae06be954f624ccbf85758a03f389ded33118c
RUN apk add --no-cache ca-certificates wget xz \
    && wget -q -O /tmp/typst.tar.xz \
       "https://github.com/typst/typst/releases/download/v${TYPST_VERSION}/typst-x86_64-unknown-linux-musl.tar.xz" \
    && echo "${TYPST_SHA256}  /tmp/typst.tar.xz" | sha256sum -c - \
    && xz -dc /tmp/typst.tar.xz | tar -xf - -C /tmp \
    && install -m 0755 /tmp/typst-x86_64-unknown-linux-musl/typst /usr/local/bin/typst \
    && mkdir -p /usr/local/share/licenses/typst \
    && cp /tmp/typst-x86_64-unknown-linux-musl/LICENSE /tmp/typst-x86_64-unknown-linux-musl/NOTICE \
          /usr/local/share/licenses/typst/ 2>/dev/null || true \
    && /usr/local/bin/typst --version

# Runtime stage
FROM alpine:3.20

# ca-certificates for outbound TLS, wget for the container health check.
RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 app

COPY --from=typst /usr/local/bin/typst /usr/local/bin/typst
COPY --from=typst /usr/local/share/licenses/typst /usr/local/share/licenses/typst

WORKDIR /app
COPY --from=builder /out/server ./server

USER app
EXPOSE 8080

CMD ["./server"]
