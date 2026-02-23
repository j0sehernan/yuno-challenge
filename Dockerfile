FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /bin/idempotency-shield ./cmd/server

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /bin/idempotency-shield /bin/idempotency-shield
COPY --from=builder /app/migrations /migrations

WORKDIR /
EXPOSE 8080
CMD ["/bin/idempotency-shield"]
