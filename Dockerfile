# SPDX-License-Identifier: Apache-2.0
# Copyright The Jaga Authors
#
# Multi-stage image, see docs/04-project-structure.md#docker:
# web UI (Node) -> jaga (Go, static) -> debian-slim + FFmpeg LGPL + go2rtc.
# Third-party binaries are pinned by version and SHA-256.

# --- web UI -----------------------------------------------------------------
FROM node:24-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund
COPY web/ ./
COPY docs/brand/tokens.css /src/docs/brand/tokens.css
RUN npm run build

# --- jaga -------------------------------------------------------------------
FROM golang:1.26-bookworm AS build
ARG VERSION=dev
ARG COMMIT=none
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -tags webui \
      -ldflags "-s -w -X github.com/ryandinulfatah12/jaga/internal/version.Version=${VERSION} -X github.com/ryandinulfatah12/jaga/internal/version.Commit=${COMMIT}" \
      -o /out/jaga ./cmd/jaga

# --- third-party binaries ---------------------------------------------------
FROM debian:bookworm-slim AS deps
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl xz-utils \
 && rm -rf /var/lib/apt/lists/*
# FFmpeg LGPL build (BtbN/FFmpeg-Builds, release branch n9.0), docs/09-legal.md#ffmpeg.
# ponytail: amd64 only; add the linuxarm64 asset + TARGETARCH switch for arm64 images.
ARG FFMPEG_BUILD=autobuild-2026-11-07-12-47
ARG FFMPEG_NAME=ffmpeg-n9.0.2-48-gd4070a14ce-linux64-lgpl-9.0
ARG FFMPEG_SHA256=e4f77079434c18b9ed6a7ebe801b098d755901c03397c1527d861e2d7c92886e
ARG GO2RTC_VERSION=v1.9.15
ARG GO2RTC_SHA256=20b46c6910734cb05ecefb234066f36868ff19b42ba1418a7a32e13a3e035712
WORKDIR /tmp
RUN curl -fsSLo ffmpeg.tar.xz "https://github.com/FFmpeg-Builds/FFmpeg-Builds/releases/download/${FFMPEG_BUILD}/${FFMPEG_NAME}.tar.xz" \
 && echo "${FFMPEG_SHA256}  ffmpeg.tar.xz" | sha256sum -c - \
 && tar -xJf ffmpeg.tar.xz \
 && mkdir -p /out/bin /out/licenses/ffmpeg /out/licenses/go2rtc \
 && cp "${FFMPEG_NAME}/bin/ffmpeg" "${FFMPEG_NAME}/bin/ffprobe" /out/bin/ \
 && cp "${FFMPEG_NAME}/LICENSE.txt" /out/licenses/ffmpeg/ \
 && /out/bin/ffmpeg -hide_banner -version > /out/licenses/ffmpeg/BUILD.txt \
 && curl -fsSLo /out/bin/go2rtc "https://github.com/AlexxIT/go2rtc/releases/download/${GO2RTC_VERSION}/go2rtc_linux_amd64" \
 && echo "${GO2RTC_SHA256}  /out/bin/go2rtc" | sha256sum -c - \
 && chmod 0755 /out/bin/* \
 && curl -fsSLo /out/licenses/go2rtc/LICENSE "https://raw.githubusercontent.com/AlexxIT/go2rtc/${GO2RTC_VERSION}/LICENSE"

# --- runtime ----------------------------------------------------------------
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 1000 --home-dir /data --shell /usr/sbin/nologin jaga \
 && mkdir -p /data && chown jaga:jaga /data
COPY --from=deps /out/bin/ /usr/local/bin/
COPY --from=deps /out/licenses/ /usr/share/doc/jaga/
COPY --from=build /out/jaga /usr/local/bin/jaga
COPY LICENSE NOTICE THIRD_PARTY_NOTICES.md /usr/share/doc/jaga/
ENV JAGA_DATA_DIR=/data \
    JAGA_FFMPEG_PATH=/usr/local/bin/ffmpeg \
    JAGA_GO2RTC_PATH=/usr/local/bin/go2rtc
USER jaga
WORKDIR /data
VOLUME /data
EXPOSE 8080 8555/tcp 8555/udp
# ponytail: TCP check on the listen port (bash /dev/tcp, no curl in the image);
# switch to GET /api/v1/system once that endpoint exists (Phase 3).
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s \
  CMD ["bash", "-c", "p=${JAGA_LISTEN:-:8080}; exec 3<>/dev/tcp/127.0.0.1/${p##*:}"]
ENTRYPOINT ["jaga"]
CMD ["start"]
