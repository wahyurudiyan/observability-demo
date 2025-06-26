# syntax=docker/dockerfile:1

# --- Build Stage ---
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY ./go.mod ./go.sum ./
RUN go mod download
COPY ./ .
RUN CGO_ENABLED=0 GOOS=linux go build -o otel-demo main.go

# --- Run Stage ---
FROM alpine:3.20
WORKDIR /app
COPY --from=builder /app/otel-demo ./otel-demo
EXPOSE 8080
CMD ["./otel-demo"]
