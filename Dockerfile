# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build without debug information to reduce binary size
RUN go build -ldflags="-s -w" -o isrv .

# Final stage
FROM alpine:latest

ARG USER_ID=1000
ARG GROUP_ID=1000

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

RUN addgroup -g $GROUP_ID -S isrv && \
    adduser -u $USER_ID -S -G isrv -H -s /sbin/nologin isrv

COPY --from=builder /app/isrv /app/isrv
COPY --from=builder /app/config.yaml.example /config/config.yaml

RUN chown -R isrv:isrv /app /config

USER isrv

CMD ["/app/isrv", "--config", "/config/config.yaml"]