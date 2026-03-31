# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

ARG BUILD_VERSION=v0.0.0-docker
ARG BUILD_COMMIT=none
ARG BUILD_PLATFORM=linux/amd64

ENV BUILD_VERSION=${BUILD_VERSION}
ENV BUILD_COMMIT=${BUILD_COMMIT}
ENV BUILD_PLATFORM=${BUILD_PLATFORM}
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Detect docker platform and set GOARCH accordingly
RUN case "$(uname -m)" in \
    x86_64)  export GOARCH=amd64 ;; \
    aarch64) export GOARCH=arm64 ;; \
    armv7l)  export GOARCH=arm   ;; \
    armv6l)  export GOARCH=arm   ;; \
    *)       echo "Unsupported architecture: $(uname -m)" && exit 1 ;; \
    esac && \
     echo "Detected architecture: $(uname -m), setting GOARCH=${GOARCH}" && \
     go env -w GOARCH=${GOARCH}

# Build without debug information to reduce binary size
RUN export BUILD_DATE=$(date -u '+%Y-%m-%d_%H:%M:%S') && \
    export BUILD_GO_VERSION=$(go version | awk '{print $3}') && \
    export BUILD_PLATFORM=$(uname -s | tr '[:upper:]' '[:lower:]')/$(uname -m) && \
    go build -ldflags="-s -w \
    -X 'github.com/markhc/isrv/internal/configuration.BuildVersion=${BUILD_VERSION}' \
    -X 'github.com/markhc/isrv/internal/configuration.BuildCommit=${BUILD_COMMIT}' \
    -X 'github.com/markhc/isrv/internal/configuration.BuildDate=${BUILD_DATE}' \
    -X 'github.com/markhc/isrv/internal/configuration.BuildGoVersion=${BUILD_GO_VERSION}' \
    -X 'github.com/markhc/isrv/internal/configuration.BuildPlatform=${BUILD_PLATFORM}'" -o isrv .

# Final stage
FROM alpine:latest

ARG USER_ID=1000
ARG GROUP_ID=1000

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

RUN addgroup -g $GROUP_ID -S isrv && \
    adduser -u $USER_ID -S -G isrv -H -s /sbin/nologin isrv

COPY --from=builder /app/isrv /app/isrv

RUN mkdir -p /config && \
    chown -R isrv:isrv /config && \
    chown -R isrv:isrv /app

USER isrv

# Disable supervisor in the docker build as auto-restart can be handled by the container environment
CMD ["/app/isrv", "--disable-supervisor"]