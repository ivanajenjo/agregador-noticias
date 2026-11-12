#!/bin/sh
# Copia de seguridad CONSISTENTE de SQLite con la app en marcha (sqlite3 .backup, seguro con WAL).
# Mantiene las ultimas KEEP copias comprimidas. No hace falta parar el contenedor.
#
#   deploy/backup.sh                       # ./backups/agregador-YYYYmmdd-HHMMSS.db.gz
#   BACKUP_DIR=/mnt/usb/agregador KEEP=14 INCLUDE_SHOTS=1 deploy/backup.sh
#
# Variables: BACKUP_DIR (def. ./backups), KEEP (def. 7), INCLUDE_SHOTS (0|1, def. 0; capturas = tar sin comprimir),
#            DATA_DIR (def. ./data). La primera vez construye una imagen local minima con sqlite3.
set -eu

cd "$(dirname "$0")/.."
DATA_DIR=${DATA_DIR:-$(pwd)/data}
BACKUP_DIR=${BACKUP_DIR:-$(pwd)/backups}
KEEP=${KEEP:-7}
INCLUDE_SHOTS=${INCLUDE_SHOTS:-0}
IMG=agregador-sqlite:3.22

DOCKER=docker
docker info >/dev/null 2>&1 || DOCKER="sudo docker"
[ -f "$DATA_DIR/agregador.db" ] || { echo "no existe $DATA_DIR/agregador.db" >&2; exit 1; }
mkdir -p "$BACKUP_DIR"

if ! $DOCKER image inspect "$IMG" >/dev/null 2>&1; then
  printf 'FROM alpine:3.22\nRUN apk add --no-cache sqlite\n' | $DOCKER build -q -t "$IMG" - >/dev/null
fi

ts=$(date +%Y%m%d-%H%M%S)
out="$BACKUP_DIR/agregador-$ts.db.gz"
tmp="$out.part"
owner=$(stat -c %u:%g "$DATA_DIR")   # mismo uid que la app para no crear -wal/-shm de root

# .backup -> /tmp del contenedor (disco, no RAM) -> integrity_check -> stdout -> gzip en el host
# shellcheck disable=SC2016
$DOCKER run --rm --user "$owner" -v "$DATA_DIR:/data" --entrypoint sh "$IMG" -c '
  set -eu
  sqlite3 /data/agregador.db ".timeout 30000" ".backup /tmp/b.db"
  [ "$(sqlite3 /tmp/b.db "PRAGMA integrity_check;")" = ok ] || { echo "integrity_check fallo" >&2; exit 1; }
  cat /tmp/b.db
' | gzip -6 > "$tmp"
mv "$tmp" "$out"
echo "backup: $out ($(du -h "$out" | cut -f1))"

if [ "$INCLUDE_SHOTS" = 1 ] && [ -d "$DATA_DIR/shots" ]; then
  tar -cf "$BACKUP_DIR/shots-$ts.tar" -C "$DATA_DIR" shots
  echo "backup: $BACKUP_DIR/shots-$ts.tar"
fi

# Rotacion: conserva las KEEP mas recientes de cada tipo.
for pat in 'agregador-*.db.gz' 'shots-*.tar'; do
  # shellcheck disable=SC2012,SC2086
  ls -1t "$BACKUP_DIR"/$pat 2>/dev/null | tail -n +"$((KEEP + 1))" | while read -r f; do rm -f -- "$f"; done
done
