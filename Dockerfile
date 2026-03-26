# ── Stage 1: Build ──────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

# Install git (needed for some go modules) and ca-certificates
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Cache dependencies first (faster rebuilds)
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the binary from cmd/server.go
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o server ./cmd/

# ── Stage 2: Runtime ─────────────────────────────────────────────────────────
FROM alpine:3.20

# ca-certificates for HTTPS calls (Meta API, Groq, etc.)
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy only the compiled binary from the builder stage
COPY --from=builder /app/server .

# Expose the default port
EXPOSE 8080

# Run the server
CMD ["./server"]
