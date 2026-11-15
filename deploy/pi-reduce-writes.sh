#!/bin/sh
# Reduce las escrituras del sistema en la SD de la Pi (no toca la app). Idempotente: se puede relanzar.
#
#   ssh -t pi 'sudo sh ~/agregador-noticias/deploy/pi-reduce-writes.sh'
#
# - journald solo en RAM (64 MiB): los logs del sistema se pierden al reiniciar.
# - rsyslog desactivado: duplicaba el journal en /var/log/{syslog,kern.log,ufw.log}.
# - ufw sin logging: el router manda multicast IGMP cada 20 s y cada bloqueo era una linea.
# - / montado con noatime: leer un fichero ya no escribe su fecha de acceso.
# Ver docs/OPERACION.md ("Desgaste de la SD").
set -eu

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || die "ejecuta como root: sudo sh $0"

echo "== journald en RAM"
mkdir -p /etc/systemd/journald.conf.d
cat > /etc/systemd/journald.conf.d/90-sd-volatile.conf <<'EOF'
# Generado por agregador-noticias/deploy/pi-reduce-writes.sh: journal solo en RAM para cuidar la SD.
[Journal]
Storage=volatile
RuntimeMaxUse=64M
EOF
systemctl restart systemd-journald
rm -rf /var/log/journal

echo "== rsyslog"
if systemctl list-unit-files rsyslog.service >/dev/null 2>&1; then
  systemctl disable --now rsyslog.service syslog.socket 2>/dev/null || systemctl disable --now rsyslog.service
fi

echo "== ufw logging"
if command -v ufw >/dev/null 2>&1; then
  ufw logging off
fi

echo "== noatime en /"
if ! awk '$2 == "/" && $4 ~ /(^|,)noatime(,|$)/ { found = 1 } END { exit !found }' /etc/fstab; then
  cp -p /etc/fstab "/etc/fstab.bak.$(date +%Y%m%d%H%M%S)"
  awk 'BEGIN { OFS = "\t" } $1 !~ /^#/ && $2 == "/" { $4 = $4 ",noatime" } { print }' /etc/fstab > /etc/fstab.new
  mv /etc/fstab.new /etc/fstab
fi
mount -o remount,noatime /
findmnt -no OPTIONS /

echo "OK. Comprueba en unas horas: cat /proc/diskstats | grep 'mmcblk0 ' (campo 10 = sectores escritos)."
