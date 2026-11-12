#!/bin/sh
# Instala/actualiza el agregador en una Raspberry Pi (Raspberry Pi OS 64-bit / Debian arm64) con Docker.
# Idempotente: se puede relanzar sin romper nada. Ejecutar desde un clon del repositorio:
#
#   deploy/pi-install.sh [--install-docker] [--chromium] [--shots] [--https] [--no-auth] [--build]
#
#   --install-docker  instala Docker con el script oficial (get.docker.com) si falta, sin preguntar
#   --chromium        usa la imagen con Chromium integrado (latest-chromium, 768 MB de limite)
#   --shots           activa tambien el sidecar Chromium (perfil `shots`; se guarda en COMPOSE_PROFILES)
#   --https           activa Caddy con HTTPS interno (perfil `https`)
#   --no-auth         no genera usuario/contrasena aleatorios en un .env nuevo
#   --build           compila la imagen en la Pi en vez de descargarla de GHCR (lento)
set -eu

INSTALL_DOCKER=0 CHROMIUM=0 SHOTS=0 HTTPS=0 AUTH=1 BUILD=0
for a in "$@"; do
  case "$a" in
    --install-docker) INSTALL_DOCKER=1 ;;
    --chromium) CHROMIUM=1 ;;
    --shots) SHOTS=1 ;;
    --https) HTTPS=1 ;;
    --no-auth) AUTH=0 ;;
    --build) BUILD=1 ;;
    -h|--help) sed -n '2,12p' "$0"; exit 0 ;;
    *) echo "opcion desconocida: $a" >&2; exit 2 ;;
  esac
done

cd "$(dirname "$0")/.."
ROOT=$(pwd)
say() { printf '==> %s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

# Fija KEY=VALUE en .env (sustituye la linea, descomentada o no, o la anade al final).
set_env() {
  if grep -Eq "^#?$1=" .env; then
    sed -i "s|^#\{0,1\}$1=.*|$1=$2|" .env
  else
    printf '%s=%s\n' "$1" "$2" >> .env
  fi
}

# --- 0. arquitectura
arch=$(uname -m)
case "$arch" in
  aarch64|arm64|x86_64) ;;
  armv7l|armv6l) die "SO de 32 bits ($arch): reinstala Raspberry Pi OS de 64 bits; no hay imagen para armv7." ;;
  *) say "arquitectura inusual: $arch (continuo)" ;;
esac

# --- 1. docker
if ! command -v docker >/dev/null 2>&1; then
  if [ "$INSTALL_DOCKER" -eq 0 ]; then
    printf 'Docker no esta instalado. Instalarlo con el script oficial https://get.docker.com ? [s/N] '
    read -r ans || ans=n
    case "$ans" in s|S|y|Y) INSTALL_DOCKER=1 ;; esac
  fi
  [ "$INSTALL_DOCKER" -eq 1 ] || die "Docker es necesario. Reintenta con --install-docker."
  say "instalando Docker (get.docker.com)"
  curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
  if [ "$(id -u)" -eq 0 ]; then sh /tmp/get-docker.sh; else sudo sh /tmp/get-docker.sh; fi
  rm -f /tmp/get-docker.sh
  if [ "$(id -u)" -ne 0 ]; then
    sudo usermod -aG docker "$(id -un)" || true
    say "anadido a 'docker'. Cierra sesion y vuelve a entrar si el siguiente paso falla por permisos."
  fi
fi
DOCKER="docker"
docker info >/dev/null 2>&1 || { DOCKER="sudo docker"; $DOCKER info >/dev/null 2>&1 || die "no puedo hablar con el daemon de Docker"; }
$DOCKER compose version >/dev/null 2>&1 || die "falta el plugin 'docker compose' (apt install docker-compose-plugin)."

# --- 2. .env
if [ ! -f .env ]; then
  say "creando .env a partir de .env.example"
  cp .env.example .env
  chmod 600 .env
  if [ "$AUTH" -eq 1 ]; then
    pass=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 20)
    set_env AGREGADOR_AUTH_USER admin
    set_env AGREGADOR_AUTH_PASS "$pass"
    say "acceso: usuario 'admin', contrasena '$pass' (guardada en .env)"
  fi
else
  say ".env ya existe: no se toca (salvo las opciones pedidas ahora)"
fi
if [ "$CHROMIUM" -eq 1 ]; then
  set_env AGREGADOR_TAG latest-chromium
  set_env AGREGADOR_TARGET chromium
  set_env AGREGADOR_MEM_LIMIT 768m
  set_env AGREGADOR_PIDS_LIMIT 512
  set_env AGREGADOR_TMP_SIZE 512m
  set_env AGREGADOR_GOMEMLIMIT 128MiB
fi
if [ "$SHOTS" -eq 1 ]; then
  ip=$(grep '^CHROME_IP=' .env | cut -d= -f2 | cut -d' ' -f1)
  set_env AGREGADOR_SCREENSHOTS_REMOTE_URL "ws://${ip:-172.29.77.10}:9222"
fi
if [ "$HTTPS" -eq 1 ]; then
  set_env AGREGADOR_BIND 127.0.0.1
  set_env AGREGADOR_HOST "$(hostname).local"
fi

# Los perfiles se guardan en .env (COMPOSE_PROFILES) para que update.sh y futuros `docker compose` los respeten.
if [ "$SHOTS" -eq 1 ] || [ "$HTTPS" -eq 1 ]; then
  profiles=""
  if [ "$SHOTS" -eq 1 ]; then profiles="shots"; fi
  if [ "$HTTPS" -eq 1 ]; then profiles="${profiles:+$profiles,}https"; fi
  set_env COMPOSE_PROFILES "$profiles"
fi
COMPOSE="$DOCKER compose"

# --- 3. datos
UID_APP=$(grep '^AGREGADOR_UID=' .env | cut -d= -f2 | cut -d' ' -f1); UID_APP=${UID_APP:-10001}
GID_APP=$(grep '^AGREGADOR_GID=' .env | cut -d= -f2 | cut -d' ' -f1); GID_APP=${GID_APP:-10001}
mkdir -p data
if [ "$(stat -c %u data)" != "$UID_APP" ]; then
  say "ajustando propietario de ./data a $UID_APP:$GID_APP"
  $DOCKER run --rm -v "$ROOT/data:/data" alpine:3.22 chown "$UID_APP:$GID_APP" /data
fi

# --- 4. imagen y arranque
if [ "$BUILD" -eq 1 ]; then
  say "compilando la imagen en local (puede tardar varios minutos)"
  $COMPOSE build
else
  say "descargando la imagen"
  $COMPOSE pull
fi
say "arrancando"
$COMPOSE up -d

# --- 5. espera a healthy
say "esperando a que la app este sana"
i=0
while [ "$i" -lt 30 ]; do
  st=$($DOCKER inspect -f '{{.State.Health.Status}}' agregador 2>/dev/null || echo starting)
  [ "$st" = healthy ] && break
  i=$((i + 1)); sleep 3
done
[ "$st" = healthy ] || { $COMPOSE logs --tail 30 agregador; die "la app no llega a 'healthy' (estado: $st)"; }

port=$(grep '^AGREGADOR_PORT=' .env | cut -d= -f2 | cut -d' ' -f1); port=${port:-8080}
ip=$(hostname -I 2>/dev/null | cut -d' ' -f1)
say "listo"
if [ "$HTTPS" -eq 1 ]; then
  echo "  Web:  https://$(hostname).local   (instala antes la CA de Caddy: ver docs/DESPLIEGUE.md)"
else
  echo "  Web:  http://${ip:-<ip-de-la-pi>}:$port   (o http://$(hostname).local:$port)"
fi
echo "  Logs: $COMPOSE logs -f agregador"
