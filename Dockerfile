# Build stage for Playwright dependencies
#
# playwright-go v0.5700.1 normally bootstraps its driver by downloading
# `builds/driver/playwright-<ver>-<os>.zip` from playwright.azureedge.net.
# That CDN path was decommissioned by Microsoft (the driver zips 404 on every
# mirror, including the current cdn.playwright.dev), so `playwright install`
# can no longer bootstrap itself. Newer playwright-go versions fixed this by
# fetching the driver from npm, but they renamed their module path and cannot
# be imported alongside scrapemate, so we stay on v0.5700.1 and build the
# driver directory by hand from reliable sources instead:
#   - playwright-core-<ver>.tgz from the npm registry (provides package/cli.js)
#   - a standalone Node.js binary from nodejs.org (self-contained, survives the
#     copy into the slim final image)
# The browser binaries themselves (builds/chromium/...) are still served fine,
# so we install them by invoking the node driver directly.
FROM ubuntu:20.04 AS playwright-deps
ARG TARGETARCH
# Keep these in sync with the playwright-community/playwright-go version in go.mod.
ARG PW_VERSION=1.57.0
ARG NODE_VERSION=20.20.2
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/browsers
ENV PLAYWRIGHT_DRIVER_PATH=/opt/pw-driver

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl wget xz-utils \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* \
    && if [ "$TARGETARCH" = "arm64" ]; then NODE_ARCH="arm64"; else NODE_ARCH="x64"; fi \
    # Standalone Node.js (bundles npm; self-contained so it survives the copy into the slim final image)
    && wget -q "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${NODE_ARCH}.tar.xz" \
    && tar -xJf "node-v${NODE_VERSION}-linux-${NODE_ARCH}.tar.xz" -C /usr/local --strip-components=1 \
    && rm "node-v${NODE_VERSION}-linux-${NODE_ARCH}.tar.xz" \
    # Build the playwright-go driver directory from npm (azureedge builds/driver CDN is dead)
    && mkdir -p "$PLAYWRIGHT_DRIVER_PATH" /opt/browsers \
    && wget -q "https://registry.npmjs.org/playwright-core/-/playwright-core-${PW_VERSION}.tgz" \
    && tar -xzf "playwright-core-${PW_VERSION}.tgz" -C "$PLAYWRIGHT_DRIVER_PATH" \
    && rm "playwright-core-${PW_VERSION}.tgz" \
    && cp /usr/local/bin/node "$PLAYWRIGHT_DRIVER_PATH/node" \
    # Install Chromium via the node driver (browser builds are still served by the current CDN)
    && "$PLAYWRIGHT_DRIVER_PATH/node" "$PLAYWRIGHT_DRIVER_PATH/package/cli.js" install chromium

# Build stage
FROM golang:1.26.3-trixie AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o /usr/bin/google-maps-scraper

# Final stage
FROM debian:trixie-slim
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/browsers
ENV PLAYWRIGHT_DRIVER_PATH=/opt/pw-driver

# Install only the necessary dependencies in a single layer
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libnss3 \
    libnspr4 \
    libatk1.0-0 \
    libatk-bridge2.0-0 \
    libcups2 \
    libdrm2 \
    libdbus-1-3 \
    libxkbcommon0 \
    libatspi2.0-0 \
    libx11-6 \
    libxcomposite1 \
    libxdamage1 \
    libxext6 \
    libxfixes3 \
    libxrandr2 \
    libgbm1 \
    libpango-1.0-0 \
    libcairo2 \
    libasound2 \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

COPY --from=playwright-deps /opt/browsers /opt/browsers
COPY --from=playwright-deps /opt/pw-driver /opt/pw-driver

RUN chmod -R 755 /opt/browsers \
    && chmod -R 755 /opt/pw-driver

COPY --from=builder /usr/bin/google-maps-scraper /usr/bin/

ENTRYPOINT ["google-maps-scraper"]
