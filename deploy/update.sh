#!/bin/sh
# Actualiza a la ultima version: git pull (si es un clon), compila la imagen, up -d y limpia imagenes viejas.
# Rollback: git checkout <commit-anterior> && docker compose build && docker compose up -d (ver docs/DESPLIEGUE.md).
set -eu

cd "$(dirname "$0")/.."
DOCKER=docker
docker info >/dev/null 2>&1 || DOCKER="sudo docker"

if [ -d .git ] && command -v git >/dev/null 2>&1; then
  git pull --ff-only || { echo "ERROR: git pull fallo (cambios locales?); no se despliega" >&2; exit 1; }
fi

old=$($DOCKER inspect -f '{{.Image}}' agregador 2>/dev/null || true)
if [ -n "$old" ]; then echo "$old" > .last-image; fi   # por si hay que volver atras
$DOCKER compose build
$DOCKER compose up -d --remove-orphans --wait --wait-timeout 180   # falla si no llega a healthy
$DOCKER image prune -f >/dev/null
$DOCKER builder prune -f --filter until=168h >/dev/null   # la cache de build crece ~1 GB por despliegue
$DOCKER compose ps
