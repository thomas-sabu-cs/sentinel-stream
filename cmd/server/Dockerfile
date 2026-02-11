# Build stage
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o server ./cmd/server/main.go

# Run stage
FROM alpine:latest
WORKDIR /root/
COPY --from=builder /app/server .
# We'll need to pass .env variables to this container
CMD ["./server"]