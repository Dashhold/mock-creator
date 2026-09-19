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

# Runtime stage
FROM alpine:3.20

# ca-certificates for outbound TLS, wget for the container health check.
RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 app

WORKDIR /app
COPY --from=builder /out/server ./server

USER app
EXPOSE 8080

CMD ["./server"]
