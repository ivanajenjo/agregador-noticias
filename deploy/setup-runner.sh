#!/bin/sh
# Instala un runner self-hosted de GitHub Actions para este repo como servicio systemd, ejecutado por el dueno
# del checkout, para que .github/workflows/deploy.yml despliegue en esta maquina al hacer push a master.
# Idempotente: se puede relanzar.
#
#   sudo RUNNER_TOKEN=<token> sh deploy/setup-runner.sh
#
# El token (caduca en 1 h) sale de GitHub -> Settings -> Actions -> Runners -> New self-hosted runner, o de
#   gh api -X POST repos/OWNER/REPO/actions/runners/registration-token --jq .token
# Si el runner ya esta registrado (existe .runner en su directorio) no hace falta.
set -eu

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || die "ejecuta como root: sudo sh $0"

REPO_DIR=$(cd "$(dirname "$0")/.." && pwd)
RUN_AS=$(stat -c %U "$REPO_DIR")
HOME_AS=$(getent passwd "$RUN_AS" | cut -d: -f6)
id -nG "$RUN_AS" | grep -qw docker || die "$RUN_AS (dueno de $REPO_DIR) debe estar en el grupo docker"
SLUG=$(sudo -u "$RUN_AS" git -C "$REPO_DIR" config --get remote.origin.url | sed -E 's#^(git@github\.com:|https://github\.com/)##; s#\.git$##')
NAME=${SLUG#*/}
DIR=${RUNNER_DIR:-$HOME_AS/actions-runner-$NAME}

# --- 1. descargar el runner
if [ ! -x "$DIR/config.sh" ]; then
  case "$(uname -m)" in
    aarch64|arm64) arch=arm64 ;;
    x86_64) arch=x64 ;;
    *) die "arquitectura no soportada: $(uname -m)" ;;
  esac
  ver=$(curl -fsSL https://api.github.com/repos/actions/runner/releases/latest | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
  [ -n "$ver" ] || die "no pude averiguar la ultima version del runner"
  echo "==> descargando actions-runner $ver en $DIR"
  install -d -o "$RUN_AS" -g "$(id -gn "$RUN_AS")" "$DIR"
  curl -fsSL "https://github.com/actions/runner/releases/download/v$ver/actions-runner-linux-$arch-$ver.tar.gz" \
    | sudo -u "$RUN_AS" tar -xz -C "$DIR"
fi
cd "$DIR"
./bin/installdependencies.sh >/dev/null

# --- 2. registrarlo en el repo
if [ ! -f .runner ]; then
  [ -n "${RUNNER_TOKEN:-}" ] || die "falta RUNNER_TOKEN (ver cabecera del script)"
  echo "==> registrando el runner en $SLUG"
  sudo -u "$RUN_AS" ./config.sh --unattended --replace --url "https://github.com/$SLUG" --token "$RUNNER_TOKEN" \
    --name "$(hostname)-$NAME" --labels pi
fi

# --- 3. servicio systemd
if [ ! -f .service ]; then
  ./svc.sh install "$RUN_AS"
fi
./svc.sh start >/dev/null
./svc.sh status | sed -n '1,4p'
echo "==> listo: los push a master se despliegan aqui (GitHub -> Actions -> Deploy)"
