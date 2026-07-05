FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend-builder

WORKDIR /app/web

RUN apk add --no-cache git

RUN npm install -g corepack@latest && corepack enable

COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY web ./
RUN pnpm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS go-builder

WORKDIR /app

ARG BUILD_VERSION=v0.0.0-docker
ARG BUILD_COMMIT=none

# Provided automatically by buildx for the platform being built.
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

ENV BUILD_VERSION=${BUILD_VERSION}
ENV BUILD_COMMIT=${BUILD_COMMIT}
ENV CGO_ENABLED=0

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY web/ ./web/
COPY --from=frontend-builder /app/web/dist ./web/dist

# Cross-compile natively for the target platform (no QEMU emulation).
# Build without debug information to reduce binary size.
RUN export GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${TARGETVARIANT#v} && \
    export BUILD_DATE=$(date -u '+%Y-%m-%d_%H:%M:%S') && \
    export BUILD_GO_VERSION=$(go version | awk '{print $3}') && \
    export BUILD_PLATFORM=${GOOS}/${GOARCH} && \
    echo "Cross-compiling for ${GOOS}/${GOARCH}${GOARM:+ (GOARM=${GOARM})}" && \
    go build -ldflags="-s -w \
    -X 'github.com/markhc/isrv/internal/configuration.BuildVersion=${BUILD_VERSION}' \
    -X 'github.com/markhc/isrv/internal/configuration.BuildCommit=${BUILD_COMMIT}' \
    -X 'github.com/markhc/isrv/internal/configuration.BuildDate=${BUILD_DATE}' \
    -X 'github.com/markhc/isrv/internal/configuration.BuildGoVersion=${BUILD_GO_VERSION}' \
    -X 'github.com/markhc/isrv/internal/configuration.BuildPlatform=${BUILD_PLATFORM}'" -o isrv ./cmd/isrv

# Final stage
FROM alpine:latest

ARG USER_ID=1000
ARG GROUP_ID=1000

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

RUN addgroup -g $GROUP_ID -S isrv && \
    adduser -u $USER_ID -S -G isrv -H -s /sbin/nologin isrv

COPY --from=go-builder /app/isrv /app/isrv

RUN mkdir -p /config && \
    chown -R isrv:isrv /config && \
    chown -R isrv:isrv /app

USER isrv

# Disable supervisor in the docker build as auto-restart can be handled by the container environment
CMD ["/app/isrv", "--disable-supervisor"]