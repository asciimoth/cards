FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
RUN go build -o /go/bin/app *.go

FROM alpine:latest

RUN apk add --no-cache ca-certificates

WORKDIR /root/

COPY static ./static
COPY templates ./templates
COPY locales ./locales
COPY --from=builder /go/bin/app .

EXPOSE 8080

ENTRYPOINT ["./app"]
