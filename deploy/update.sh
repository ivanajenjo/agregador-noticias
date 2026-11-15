#!/bin/sh
# Actualiza a la ultima version: git pull (si es un clon), compila la imagen, up -d y limpia imagenes viejas.
# Rollback: git checkout <commit-anterior> y vuelve a lanzar este script (ver docs/DESPLIEGUE.md).
set -eu

cd "$(dirname "$0")/.."
DOCKER=docker
docker info >/dev/null 2>&1 || DOCKER="sudo docker"

if [ -d .git ] && command -v git >/dev/null 2>&1; then
  git pull --ff-only || echo "aviso: git pull fallo (cambios locales?); sigo con los ficheros actuales" >&2
fi

old=$($DOCKER inspect -f '{{.Image}}' agregador 2>/dev/null || true)
if [ -n "$old" ]; then echo "$old" > .last-image; fi   # por si hay que volver atras
$DOCKER compose build
$DOCKER compose up -d --remove-orphans
$DOCKER image prune -f >/dev/null
$DOCKER compose ps
