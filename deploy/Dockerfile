# syntax=docker/dockerfile:1.7
#
# Imagen multi-arch (linux/amd64, linux/arm64) del agregador.
#
#   docker buildx build -f deploy/Dockerfile --target slim     ...   (por defecto; sin navegador)
#   docker buildx build -f deploy/Dockerfile --target chromium ...   (incluye Chromium para las capturas)
#
# El binario se compila SIEMPRE en la plataforma del builder (rapido) y se
# cross-compila a la plataforma destino: no hace falta QEMU para compilar Go.
# (QEMU solo se usa para el `apk add` de la etapa final cuando se construye arm64 en una maquina x86.)

ARG GO_VERSION=1.26
ARG ALPINE_VERSION=3.22

# ---------------------------------------------------------------- build
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    V="${VERSION}"; C="$(printf %.7s "${COMMIT}")"; [ "${C}" = "unknown" ] || V="${VERSION}-${C}"; \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=${V}" \
      -o /out/agregador ./cmd/agregador

# ---------------------------------------------------------------- base comun
FROM alpine:${ALPINE_VERSION} AS base
# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S -g 10001 app && adduser -S -D -H -u 10001 -G app app \
 && mkdir -p /data /config && chown app:app /data
COPY --from=build /out/agregador /app/agregador
COPY config/sources.yaml config/config.example.yaml /app/config/
WORKDIR /app
# Sistema de ficheros de solo lectura: lo unico escribible es /data (volumen) y /tmp (tmpfs).
# HOME=/tmp para que Chromium (si existe) escriba su perfil temporal alli.
ENV AGREGADOR_DATA_DIR=/data \
    AGREGADOR_SOURCES_FILE=/app/config/sources.yaml \
    AGREGADOR_CONFIG=/config/config.yaml \
    AGREGADOR_LISTEN=:8080 \
    TZ=Europe/Madrid \
    HOME=/tmp
USER 10001:10001
VOLUME ["/data"]
EXPOSE 8080
# /healthz esta exento de autenticacion. El puerto sale de AGREGADOR_LISTEN (busybox wget, sin herramientas extra).
HEALTHCHECK --interval=60s --timeout=5s --start-period=30s --retries=3 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${AGREGADOR_LISTEN##*:}/healthz" || exit 1
STOPSIGNAL SIGTERM
ENTRYPOINT ["/app/agregador"]

# ---------------------------------------------------------------- chromium
# Chromium se lanza bajo demanda solo durante las capturas (cron) y se cierra despues.
# En un contenedor sin privilegios hace falta AGREGADOR_SCREENSHOTS_NO_SANDBOX=true.
FROM base AS chromium
USER root
# hadolint ignore=DL3018
RUN apk add --no-cache chromium font-noto-emoji font-liberation font-freefont
ENV AGREGADOR_SCREENSHOTS_CHROME_PATH=/usr/bin/chromium \
    AGREGADOR_SCREENSHOTS_NO_SANDBOX=true \
    GOMEMLIMIT=128MiB
USER 10001:10001

# ---------------------------------------------------------------- slim (por defecto: ultima etapa)
FROM base AS slim
ARG VERSION=dev
ARG COMMIT=unknown
ENV GOMEMLIMIT=96MiB
LABEL org.opencontainers.image.title="agregador-noticias" \
      org.opencontainers.image.source="https://github.com/ivanajenjo/agregador-noticias" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"
