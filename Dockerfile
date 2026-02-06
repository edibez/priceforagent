# Build stage
FROM golang:1.22-alpine AS builder

# Install build dependencies for CGO (SQLite)
RUN apk add --no-cache gcc musl-dev

WORKDIR /app

# Install dependencies
COPY go.mod go.sum* ./
RUN go mod download

# Copy source
COPY . .

# Build with CGO enabled for SQLite
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o /server ./cmd/server

# Runtime stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Create data directory
RUN mkdir -p /data

# Copy binary and API spec
COPY --from=builder /server .
COPY --from=builder /app/api ./api

# Non-root user
RUN adduser -D -g '' appuser && chown -R appuser:appuser /data
USER appuser

EXPOSE 8080

ENV DB_PATH=/data/priceforagent.db
ENV REDIS_ADDR=host.docker.internal:6379

CMD ["./server"]
