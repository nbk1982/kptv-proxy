# Build stage - pinned to the build platform so Go cross-compiles natively
FROM --platform=$BUILDPLATFORM docker.io/golang:1.27.0-alpine AS builder

ARG TARGETARCH

RUN apk add --no-cache git ca-certificates

# setup the work directory
WORKDIR /app

# copy in our modulators
COPY go.mod go.sum ./

# download the modules necessary
RUN go mod download

# copy in the source code
COPY . .

# build the app
RUN VERSION="v$(date -u +%Y%m%d%H.%M)" && \
    BUILDDATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" && \
    COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)" && \
    echo ">>> VERSION=$VERSION BUILDDATE=$BUILDDATE COMMIT=$COMMIT ARCH=$TARGETARCH <<<" && \
    CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build \
    -ldflags="-s -w -X kptv-proxy/work/app.Version=$VERSION -X kptv-proxy/work/app.BuildDate=$BUILDDATE -X kptv-proxy/work/app.Commit=$COMMIT" \
    -trimpath -o kptv-proxy .

# Final stage - keep all GPU drivers
FROM docker.io/debian:trixie-slim

# Image provenance. org.opencontainers.image.source is what links the published
# package to its repository on GHCR; override IMAGE_SOURCE at build time to
# retarget the image at a different fork.
ARG IMAGE_SOURCE=https://github.com/nbk1982/kptv-proxy
LABEL org.opencontainers.image.source="${IMAGE_SOURCE}" \
      org.opencontainers.image.url="${IMAGE_SOURCE}" \
      org.opencontainers.image.documentation="${IMAGE_SOURCE}/blob/main/readme.md" \
      org.opencontainers.image.title="kptv-proxy" \
      org.opencontainers.image.description="IPTV proxy with per-source content classification and filtering, EPG merging and Xtream Codes output" \
      org.opencontainers.image.licenses="MIT"

# Install curl and necessities curl in one layer
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* \
    && rm -rf /usr/share/doc /usr/share/man \
    && find /usr/share/locale -mindepth 1 -maxdepth 1 ! -name 'en' ! -name 'en_US' -exec rm -rf {} + 2>/dev/null || true

# Create groups in one layer
RUN groupadd --system --gid 107 render \
    && groupadd --gid 1000 kptv \
    && useradd --uid 1000 --gid 1000 --create-home --shell /bin/bash kptv \
    && usermod -a -G video,render kptv \
    && mkdir -p /dev/dri /settings \
    && chown -R kptv:kptv /settings \
    && chmod 775 /settings

# The admin interface and the offline clip are embedded in the binary, so
# nothing needs to be copied into /static.
COPY --from=builder /app/kptv-proxy /usr/local/bin/kptv-proxy

WORKDIR /workspace
USER kptv

ENV PATH="/usr/local/bin:${PATH}" GOGC=100

EXPOSE 8080
CMD ["/usr/local/bin/kptv-proxy"]