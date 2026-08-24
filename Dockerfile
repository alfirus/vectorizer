# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go module files first (for better caching)
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o vectorizer .

# Final stage
FROM alpine:3.21

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/vectorizer .

EXPOSE 8091 50051

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:8091/api/v1/health || exit 1

CMD ["./vectorizer"]
