FROM golang:1.22-alpine AS builder
WORKDIR /app

# Copy source first so go mod tidy can resolve imports, then generate go.sum
COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o main .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/main .
EXPOSE 8000
CMD ["./main"]
