# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install dependencies
COPY go.mod go.sum* ./
RUN go mod download

# Copy source
COPY . .

# Build with optimizations
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /server ./cmd/server

# Runtime stage - minimal image
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy binary and API spec
COPY --from=builder /server .
COPY --from=builder /app/api ./api

# Non-root user
RUN adduser -D -g '' appuser
USER appuser

EXPOSE 8080

CMD ["./server"]
