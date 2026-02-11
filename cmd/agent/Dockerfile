# Build stage
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o agent ./cmd/agent/main.go

# Run stage
FROM alpine:latest
WORKDIR /root/
COPY --from=builder /app/agent .
CMD ["./agent"]