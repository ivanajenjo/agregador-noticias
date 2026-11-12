# Despliegue en Raspberry Pi con Docker

Guía para poner el agregador en una **Raspberry Pi compartida con otros proyectos**, con el LLM en un Mac/PC aparte.
Todo está pensado para consumir poco: la app ronda 50–100 MB de RAM, escribe poco a disco y no tiene privilegios.

- [1. Requisitos](#1-requisitos)
- [2. Instalación paso a paso](#2-instalación-paso-a-paso)
- [3. Configuración (`.env`)](#3-configuración-env)
- [4. El LLM en el Mac/PC](#4-el-llm-en-el-macpc)
- [5. Capturas de portada](#5-capturas-de-portada)
- [6. HTTPS y PWA](#6-https-y-pwa)
- [7. Autenticación y seguridad](#7-autenticación-y-seguridad)
- [8. Convivir con otros proyectos](#8-convivir-con-otros-proyectos)
- [9. Copias de seguridad y restauración](#9-copias-de-seguridad-y-restauración)
- [10. Actualizar y volver atrás](#10-actualizar-y-volver-atrás)
- [11. Logs y diagnóstico](#11-logs-y-diagnóstico)
- [12. Checklist de pruebas (smoke test)](#12-checklist-de-pruebas-smoke-test)
- [13. Desinstalar](#13-desinstalar)

## 1. Requisitos

| Elemento | Mínimo | Recomendado |
|---|---|---|
| Hardware | Raspberry Pi 4 (2 GB) | Pi 4/5 con 4 GB+ |
| Sistema | Raspberry Pi OS **64-bit** / Debian arm64 (Bookworm o posterior) | igual |
| Almacenamiento | tarjeta SD de calidad (A1/A2) | **SSD/USB 3** para `./data` |
| Software | Docker Engine 24+ con el plugin `docker compose` v2 | (lo instala `deploy/pi-install.sh`) |
| Red | la Pi y el Mac en la misma LAN | IP fija/reserva DHCP para ambos |

Comprueba que el sistema es de 64 bits: `uname -m` debe decir `aarch64`. Con `armv7l` (32 bits) **no hay imagen**: reinstala el SO de 64 bits.

**RAM (aprox.)**

| Componente | Reposo | Pico |
|---|---|---|
| `agregador` (imagen `slim`) | 50–100 MB | ~150 MB durante una ingesta |
| `agregador` con Chromium integrado (`-chromium`) | 50–100 MB | **400–700 MB** solo durante las capturas (unos minutos, 3 veces al día) |
| Sidecar `chrome` (perfil `shots`) | 150–300 MB **siempre** | 500–768 MB |
| `caddy` (perfil `https`) | ~15 MB | ~30 MB |

**Disco (estimación, ajústala a tu catálogo).** Hay 47 medios en `config/sources.yaml`, 22 con `screenshot: true`.

- Capturas: ~20 medios × 3 al día × 300–600 KB ≈ **18–36 MB/día ≈ 0,5–1 GB/mes**. Con la retención por defecto (90 días,
  `AGREGADOR_SCREENSHOTS_RETENTION=2160h`) se estabiliza en **~1,6–3,2 GB**.
- Artículos: 500–1500 al día × ~10–25 KB (texto, índice FTS y vectores float32 de 768 dimensiones = 3 KB por trozo) ≈ **5–35 MB/día**,
  es decir 2–12 GB/año. La base de datos no se purga sola.
- Reserva **10 GB** para empezar; con SD de 32 GB conviene vigilar `du -sh data` y bajar la retención o mover `./data` a un SSD.
- Para gastar menos: `screenshot: false` en medios que no te interesen, `AGREGADOR_SCREENSHOTS_QUALITY=60`, retención `720h` (30 días).

## 2. Instalación paso a paso

### Opción A: script (recomendada)

```bash
git clone https://github.com/ivanajenjo/agregador-noticias.git
cd agregador-noticias
deploy/pi-install.sh --install-docker        # añade --chromium, --shots o --https según lo que necesites
```

El script es idempotente (puedes relanzarlo): instala Docker solo si falta (con tu confirmación o `--install-docker`), crea `.env`
desde `.env.example` con **usuario `admin` y contraseña aleatoria** (la imprime; `--no-auth` lo evita), da a `./data` el propietario correcto,
descarga la imagen, arranca y espera a que esté `healthy`. Al final imprime la URL.

Opciones: `--chromium` (imagen con navegador integrado), `--shots` (sidecar Chromium), `--https` (Caddy), `--build` (compila en la Pi en vez de descargar).

### Opción B: a mano

```bash
curl -fsSL https://get.docker.com | sudo sh && sudo usermod -aG docker "$USER"   # cierra sesión y vuelve a entrar
git clone https://github.com/ivanajenjo/agregador-noticias.git && cd agregador-noticias
cp .env.example .env && chmod 600 .env && nano .env                              # ver sección 3
mkdir -p data && docker run --rm -v "$PWD/data:/data" alpine:3.22 chown 10001:10001 /data
docker compose pull && docker compose up -d
docker compose ps        # esperar "healthy"
```

Abre `http://IP-DE-LA-PI:8080` (o el `AGREGADOR_PORT` que hayas puesto).

### Imágenes en GHCR (privadas por defecto)

GitHub Actions (`.github/workflows/release.yml`) publica `ghcr.io/ivanajenjo/agregador-noticias` para `linux/arm64` y `linux/amd64`:

| Etiqueta | Contenido |
|---|---|
| `latest`, `sha-1234567` | último `main` (slim) |
| `v1.2.3`, `1.2` | versión etiquetada (`make release VERSION=v1.2.3`) |
| `latest-chromium`, `v1.2.3-chromium`, … | igual, con Chromium + fuentes |

La primera vez el paquete es **privado**: o lo haces público (GitHub → Packages → el paquete → *Package settings* → *Change visibility*),
o en la Pi haces `docker login ghcr.io -u TU_USUARIO` con un token con `read:packages`. Sin imagen publicada, usa `deploy/pi-install.sh --build`
(compila en la Pi: ~5 min la primera vez, 1–2 GB de espacio temporal).

## 3. Configuración (`.env`)

Compose lee `.env` automáticamente. **Todas las variables están documentadas en [`.env.example`](../.env.example)**; las más importantes:

| Variable | Defecto | Para qué |
|---|---|---|
| `AGREGADOR_PORT` | `8080` | puerto del host (cámbialo si choca con otro proyecto) |
| `AGREGADOR_BIND` | `0.0.0.0` | `127.0.0.1` si solo se entra por Caddy |
| `TZ` | `Europe/Madrid` | zona horaria de los cron (ingesta, capturas) |
| `AGREGADOR_AUTH_USER` / `_PASS` | vacío | basic auth (vacío = abierto) |
| `AGREGADOR_EMBEDDINGS_BASE_URL` / `_MODEL` | vacío / `nomic-embed-text` | embeddings en el Mac |
| `AGREGADOR_CHAT_BASE_URL` / `_MODEL` | vacío | RAG en el Mac |
| `AGREGADOR_INGEST_CRON` | `*/30 * * * *` | frecuencia de ingesta |
| `AGREGADOR_SCREENSHOTS_CRON` | `0 9,17,22 * * *` | horas de captura |
| `AGREGADOR_SCREENSHOTS_REMOTE_URL` | vacío | Chromium remoto/sidecar (`ws://IP:9222`) |
| `AGREGADOR_MEM_LIMIT`, `_CPUS` | `256m`, `1.0` | límites del contenedor |
| `AGREGADOR_TAG` | `latest` | `latest-chromium`, `v1.2.3`… |

Sin `AGREGADOR_*_BASE_URL` la app funciona igual (ingesta + búsqueda de texto); se activan embeddings y RAG al configurarlos.

Para ajustes que no tienen variable (concurrencia, `user_agent`, tamaño de captura…) copia `config/config.example.yaml` a `config/config.yaml`:
`./config` se monta en `/config` (solo lectura) y la app lee `/config/config.yaml`. **Las variables `AGREGADOR_*` siempre ganan al YAML.**
`config/sources.yaml` (medios y feeds) vive en el mismo directorio: edítalo y `docker compose restart agregador`.

## 4. El LLM en el Mac/PC

El agregador habla con cualquier servidor OpenAI-compatible. Ejemplo con **Ollama** en el Mac:

```bash
# En el Mac
brew install ollama            # o la app de ollama.com
ollama pull nomic-embed-text   # embeddings (768 dim, ~270 MB)
ollama pull qwen3:14b          # chat/RAG; elige según tu RAM (qwen3:8b si vas justo)
```

1. **Escuchar en la LAN** (por defecto solo en 127.0.0.1):
   - App de Ollama (macOS): `launchctl setenv OLLAMA_HOST "0.0.0.0:11434"` y reinicia Ollama.
   - Servicio con `brew services`: añade `OLLAMA_HOST=0.0.0.0:11434` al entorno del servicio y reinícialo.
   - Mantener el modelo cargado entre peticiones: `launchctl setenv OLLAMA_KEEP_ALIVE "30m"`.
2. **Cortafuegos de macOS**: Ajustes del sistema → Red → Cortafuegos: permite conexiones entrantes a Ollama. Idealmente limita el acceso
   a la IP de la Pi (`pf`/router): el puerto 11434 no tiene autenticación.
3. **Usa la IP del Mac, no `macbook.local`.** Reserva una IP fija en el router. Los contenedores **no resuelven nombres mDNS `.local`**
   (musl/Go usan DNS clásico, no Bonjour), así que `http://macbook.local:11434` funciona en tu portátil pero falla dentro de Docker.
   Alternativas válidas: un nombre del DNS del router, MagicDNS de Tailscale, o `extra_hosts: ["macbook.local:192.168.1.50"]` en un
   `docker-compose.override.yml`.
4. Configura en `.env`:
   ```
   AGREGADOR_EMBEDDINGS_BASE_URL=http://192.168.1.50:11434/v1
   AGREGADOR_EMBEDDINGS_MODEL=nomic-embed-text
   AGREGADOR_CHAT_BASE_URL=http://192.168.1.50:11434/v1
   AGREGADOR_CHAT_MODEL=qwen3:14b
   ```
   y `docker compose up -d`. LM Studio usa el puerto 1234 (`http://IP:1234/v1`) y activa "Serve on local network".
5. **Prueba desde dentro del contenedor** (el camino real):
   ```bash
   docker compose exec agregador wget -qO- http://192.168.1.50:11434/v1/models
   ```

**Qué pasa si el Mac duerme o se apaga:** la ingesta y las capturas siguen; la búsqueda cae a texto (FTS); los embeddings pendientes
se completan solos cuando vuelve (la página `/estado` muestra el estado del LLM). Para que no duerma mientras sirve:
Ajustes → Batería/Energía → "Evitar que el Mac entre en reposo automáticamente con la pantalla apagada" y "Activar Wake for network access",
o `caffeinate -s &` (en corriente) / `sudo pmset -c sleep 0`.

## 5. Capturas de portada

Un Chromium sin interfaz abre cada portada, acepta cookies y guarda un JPEG. No hay navegador residente: tres formas de proveerlo
(elige **una**; si no eliges ninguna, desactiva con `AGREGADOR_SCREENSHOTS_ENABLED=false` para no ver errores en cada cron).

| Opción | Cómo | RAM en la Pi | Notas |
|---|---|---|---|
| **A. Imagen `-chromium`** (recomendada si hay ≥ 4 GB) | `AGREGADOR_TAG=latest-chromium` + límites de abajo | 0 en reposo; 400–700 MB solo durante la captura | Alpine + Chromium + fuentes (Noto emoji, Liberation, FreeFont): los sitios en español se ven bien. La imagen pesa ~340 MB. |
| **B. Sidecar** `chrome` | `COMPOSE_PROFILES=shots` + `AGREGADOR_SCREENSHOTS_REMOTE_URL=ws://172.29.77.10:9222` | 150–300 MB siempre encendido (768 MB tope) | Imagen `chromedp/headless-shell` (multi-arch), pocas fuentes: peor tipografía. |
| **C. Chromium en el Mac** | un Chromium remoto con CDP en el Mac y `AGREGADOR_SCREENSHOTS_REMOTE_URL=ws://IP-MAC:9222` | 0 | Solo captura si el Mac está despierto. El CDP no tiene auth: solo LAN. |

**A. Imagen con Chromium.** En `.env`:
```
AGREGADOR_TAG=latest-chromium
AGREGADOR_MEM_LIMIT=768m
AGREGADOR_PIDS_LIMIT=512
AGREGADOR_TMP_SIZE=512m
AGREGADOR_GOMEMLIMIT=128MiB
```
(`deploy/pi-install.sh --chromium` lo escribe.) La imagen ya fija `AGREGADOR_SCREENSHOTS_CHROME_PATH=/usr/bin/chromium` y
`AGREGADOR_SCREENSHOTS_NO_SANDBOX=true` (el sandbox de Chromium necesita privilegios que el contenedor no tiene: `cap_drop: ALL`).
La app ya lanza Chromium con `--disable-dev-shm-usage` (usa `/tmp`, que es un tmpfs), así que no hace falta `shm_size`.
El perfil temporal de Chromium vive en `/tmp` (RAM): por eso `AGREGADOR_TMP_SIZE=512m`. Cuenta contra el límite de memoria del contenedor.

**B. Sidecar.** `docker compose --profile shots up -d` (o `COMPOSE_PROFILES=shots` en `.env`) y pon la URL en `.env`.
**Importante:** Chromium rechaza peticiones CDP cuya cabecera `Host` no sea una IP o `localhost`, por eso la app lo contacta por **IP fija**
(`ws://172.29.77.10:9222`, dentro de la red `agregador`, subred `172.29.77.0/24`), no por `ws://chrome:9222`. Si esa subred choca con otra de tu Pi, cambia
`AGREGADOR_SUBNET` y `CHROME_IP` en `.env`. El puerto 9222 no se publica en el host.

**C. Chromium en el Mac.** Lo más fiable es un contenedor en el Mac: `docker run -d --init -p 9222:9222 --shm-size 1g chromedp/headless-shell:158.0.8127.18`
y `AGREGADOR_SCREENSHOTS_REMOTE_URL=ws://IP-DEL-MAC:9222` (de nuevo **IP**, no `.local`). Con Chrome de escritorio, `--remote-debugging-address=0.0.0.0` solo se respeta en algunos modos.

**Afinar para una Pi compartida**
- Menos horas: `AGREGADOR_SCREENSHOTS_CRON="0 9,21 * * *"` (2 al día) o una sola, en horas en que la Pi esté tranquila.
- Menos medios: pon `screenshot: false` en `config/sources.yaml` (las capturas se hacen de una en una, el tiempo y la RAM escalan con el número).
- Menos disco: `AGREGADOR_SCREENSHOTS_QUALITY=60` y `AGREGADOR_SCREENSHOTS_RETENTION=720h`.
- Sin capturas: `AGREGADOR_SCREENSHOTS_ENABLED=false`.
- Si el Pi se queda sin memoria durante una captura (`docker inspect agregador --format '{{.State.OOMKilled}}'` → `true`), sube `AGREGADOR_MEM_LIMIT` o pasa a la opción C.

## 6. HTTPS y PWA

La PWA (instalar en el móvil, service worker) exige **HTTPS** (excepto `localhost`). El perfil `https` añade **Caddy**, que firma un certificado con una CA interna propia.

```bash
# .env
COMPOSE_PROFILES=https
AGREGADOR_BIND=127.0.0.1            # la app solo se alcanza por Caddy
AGREGADOR_HOST=raspberrypi.local    # el nombre con el que entras (mDNS de la Pi: <hostname>.local)
docker compose up -d
```

Entra en `https://raspberrypi.local`. El navegador avisará hasta que confíes en la CA de Caddy; exporta su certificado raíz e instálalo en cada dispositivo:

```bash
docker compose cp caddy:/data/caddy/pki/authorities/local/root.crt ./caddy-root.crt
```
- iOS: envía el archivo → Ajustes → "Perfil descargado" → instalar, y luego Ajustes → General → Información → Ajustes de confianza de certificados → activar.
- Android: Ajustes → Seguridad → Cifrado y credenciales → Instalar un certificado → Certificado de CA.
- macOS: abrir en Llaveros → "Confiar siempre". 

Para el nombre `.local` los clientes usan mDNS (la Pi lo anuncia con avahi). Alternativas sin instalar CA:
- **Tailscale** (la más cómoda para entrar también desde fuera): `sudo tailscale up`, `sudo tailscale cert mipi.tu-tailnet.ts.net`, y en `deploy/Caddyfile` usa
  `mipi.tu-tailnet.ts.net { tls /ruta/cert.crt /ruta/cert.key ... }` o directamente `tailscale serve --bg --https=443 http://127.0.0.1:8080`.
- **DuckDNS + DNS-01** (certificado real de Let's Encrypt para un nombre de LAN): requiere una imagen de Caddy con el plugin DNS; no está incluida.

Puertos 80/443 ocupados por otro proyecto: cambia `CADDY_HTTP_PORT`/`CADDY_HTTPS_PORT` y entra con `https://host:8443`
(Caddy redirigirá HTTP→HTTPS al 443 estándar; usa directamente la URL HTTPS) o, mejor, usa el proxy que ya tengas (sección 8).

## 7. Autenticación y seguridad

- `AGREGADOR_AUTH_USER`/`AGREGADOR_AUTH_PASS` activan HTTP basic auth en toda la web y la API (`/healthz` queda abierto para el healthcheck). `pi-install.sh` genera una contraseña aleatoria.
  Basic auth va en claro por HTTP: úsalo con HTTPS (sección 6) si hay dispositivos no fiables en la red.
- **No expongas la app a Internet** (port-forwarding del router) sin autenticación **y** HTTPS. Para acceso remoto usa Tailscale/WireGuard.
- El contenedor corre como usuario sin privilegios (uid 10001), `read_only: true`, `cap_drop: ALL`, `no-new-privileges`, sin `privileged`, límites de RAM/CPU/PIDs, y solo escribe en `/data` y `/tmp`.
- `.env` contiene secretos (contraseña, API keys): `chmod 600 .env` (el script lo hace) y no lo subas a git (ya está en `.gitignore`).
- Los puertos del Mac (Ollama 11434, CDP 9222) no tienen autenticación: ábrelos solo a la IP de la Pi.
- La app hace peticiones salientes a los medios (RSS, páginas, capturas) respetando robots.txt y un retardo por host.

## 8. Convivir con otros proyectos

- **Puertos:** publica en otro puerto con `AGREGADOR_PORT=8090`. Comprueba los ocupados con `ss -ltnp`.
- **Red:** la app usa su propia red `agregador` (subred 172.29.77.0/24, configurable); no toca el resto. Si otro proyecto ya usa esa subred, cambia `AGREGADOR_SUBNET`/`CHROME_IP`.
- **Recursos:** `mem_limit` 256 MB (768 MB con Chromium), `cpus` 1.0, `pids_limit` y logs rotados (3×10 MB) acotan el impacto. Suma los límites de todos tus proyectos frente a la RAM de la Pi (`free -h`); sin swap un OOM mata procesos de otros contenedores.
- **Proxy inverso compartido** (Traefik, Nginx Proxy Manager, Caddy propio): no actives el perfil `https`, deja `AGREGADOR_BIND=127.0.0.1` y haz que ese proxy apunte a `127.0.0.1:AGREGADOR_PORT`; o conecta el contenedor a la red del proxy en un `docker-compose.override.yml`:
  ```yaml
  services:
    agregador:
      networks: [agregador, proxy]
  networks:
    proxy:
      external: true
  ```
  y el proxy usa `http://agregador:8080`.
- **SD y desgaste:** logs rotados, SQLite en WAL y capturas JPEG comprimidas minimizan escrituras; para uso intensivo mueve `./data` a un SSD (`ln -s` o monta el disco en `./data`).
- **Arranque:** Docker arranca los contenedores solos tras reiniciar la Pi gracias a `restart: unless-stopped`; **no hace falta unidad systemd** (hay una opcional en `deploy/systemd/agregador-compose.service`).

## 9. Copias de seguridad y restauración

```bash
deploy/backup.sh                       # ./backups/agregador-YYYYmmdd-HHMMSS.db.gz
BACKUP_DIR=/mnt/usb/agr KEEP=14 INCLUDE_SHOTS=1 deploy/backup.sh
```
Usa `sqlite3 .backup` en un contenedor desechable (copia **consistente con la app en marcha**, verifica `PRAGMA integrity_check`) y conserva las últimas `KEEP`. No copies `agregador.db` a mano mientras la app corre (el modo WAL deja datos en `-wal`).
Las capturas (`data/shots`, JPEG ya comprimidos) solo se copian con `INCLUDE_SHOTS=1`; son recuperables en el sentido de que se vuelven a generar, pero no el histórico.

**Programarlo** — con cron (`crontab -e`):
```cron
30 3 * * * cd /home/pi/agregador-noticias && KEEP=14 deploy/backup.sh >> /var/log/agregador-backup.log 2>&1
```
o con systemd (`deploy/systemd/`; edita `User`/`WorkingDirectory`):
```bash
sudo cp deploy/systemd/agregador-backup.* /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now agregador-backup.timer
```
Copia también `./backups` fuera de la Pi (rsync a otro equipo/NAS): un backup en la misma SD no protege de su fallo.

**Restaurar:**
```bash
docker compose stop agregador
mv data/agregador.db data/agregador.db.old; rm -f data/agregador.db-wal data/agregador.db-shm
gunzip -c backups/agregador-20260101-033000.db.gz > data/agregador.db
docker run --rm -v "$PWD/data:/data" alpine:3.22 chown 10001:10001 /data/agregador.db
docker compose start agregador
# capturas (si hiciste copia): tar -xf backups/shots-XXXX.tar -C data
```

## 10. Actualizar y volver atrás

```bash
deploy/update.sh        # git pull + docker compose pull + up -d + prune de imágenes viejas
```
Hazlo después de un backup si la versión cambia el esquema de datos. Las migraciones de SQLite las aplica la app al arrancar.

**Volver atrás:** fija la versión anterior en `.env` y recrea (las imágenes `vX.Y.Z` y `sha-xxxxxxx` no cambian):
```bash
sed -i 's/^AGREGADOR_TAG=.*/AGREGADOR_TAG=v1.2.2/' .env && docker compose up -d
```
Si la nueva versión migró la base de datos, restaura también el backup anterior (sección 9). `update.sh` guarda el id de la imagen previa en `.last-image` (`docker image ls` para referencia).

## 11. Logs y diagnóstico

```bash
docker compose logs -f --tail 100 agregador     # make logs
docker compose ps                               # estado + health
docker stats --no-stream                        # RAM/CPU reales
docker compose exec agregador wget -qO- http://127.0.0.1:8080/healthz
```
Los logs usan `json-file` con rotación (3 × 10 MB). La página `/estado` muestra el estado de los trabajos (ingesta, capturas) y del LLM.

| Síntoma | Causa probable | Solución |
|---|---|---|
| `permission denied` / `unable to open database file` al arrancar | `./data` no pertenece al uid 10001 | `docker run --rm -v "$PWD/data:/data" alpine:3.22 chown 10001:10001 /data` |
| Contenedor `unhealthy` | app caída o puerto distinto | `docker compose logs agregador`; `AGREGADOR_LISTEN` debe coincidir con el puerto interno (8080) |
| `exec format error` | imagen de otra arquitectura / SO de 32 bits | usa Raspberry Pi OS 64-bit; `uname -m` = `aarch64` |
| `pull access denied` / `unauthorized` | paquete GHCR privado o sin publicar | hazlo público, `docker login ghcr.io`, o `deploy/pi-install.sh --build` |
| Embeddings/chat "LLM no disponible" | Mac dormido, firewall, URL con `.local` | prueba `docker compose exec agregador wget -qO- http://IP:11434/v1/models`; usa IP; revisa `OLLAMA_HOST=0.0.0.0` |
| `no such host` con `macbook.local` | los contenedores no hacen mDNS | usa la IP o `extra_hosts` (sección 4) |
| Capturas: `no Chrome/Chromium binary found` | imagen `slim` sin `REMOTE_URL` | usa `latest-chromium` o el sidecar (sección 5), o desactiva capturas |
| Capturas: `start browser` / 500 con el sidecar | URL con nombre en vez de IP (cabecera Host) | `AGREGADOR_SCREENSHOTS_REMOTE_URL=ws://172.29.77.10:9222` y perfil `shots` activo |
| Chromium se cierra / contenedor reiniciado en capturas | OOM | `docker inspect agregador --format '{{.State.OOMKilled}}'`; sube `AGREGADOR_MEM_LIMIT`/`TMP_SIZE` o reduce medios con `screenshot: true` |
| Captura con cuadrados en vez de emojis/letras raras | faltan fuentes (sidecar) | usa la imagen `-chromium` |
| `address already in use` | puerto ocupado | cambia `AGREGADOR_PORT`/`CADDY_*_PORT` |
| La PWA no se puede instalar | no hay HTTPS | sección 6 |
| El navegador avisa de certificado en `https://raspberrypi.local` | CA interna de Caddy sin confiar | instala `root.crt` (sección 6) |
| `Pool overlaps with other pool` al hacer `up` | subred 172.29.77.0/24 en uso | cambia `AGREGADOR_SUBNET`/`CHROME_IP` |
| Hora de capturas desplazada | zona horaria | `TZ=Europe/Madrid` en `.env` |
| 401 en todo | basic auth activa | `AGREGADOR_AUTH_USER/PASS` (en `.env`); `/healthz` siempre abierto |

## 12. Checklist de pruebas (smoke test)

1. `docker compose config -q` no da errores.
2. `docker compose up -d && docker compose ps`: `agregador` pasa a **healthy** en < 1 min.
3. `curl -s http://localhost:${AGREGADOR_PORT:-8080}/healthz` devuelve `ok`.
4. Con auth: `curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/` da `401` y con `-u usuario:clave` da `200`.
5. En 2–3 minutos aparecen artículos en la portada (primera ingesta inmediata); en logs: `sources loaded`, `listening`.
6. `docker compose exec agregador wget -qO- http://IP-MAC:11434/v1/models` lista los modelos (si usas LLM); `/estado` muestra el LLM disponible.
7. Búsqueda de una palabra en la UI devuelve resultados (FTS); tras unos minutos con el Mac despierto, la búsqueda semántica y el RAG responden.
8. Capturas: lanza `curl -u usuario:clave -X POST http://localhost:8080/api/jobs/screenshots` (o espera al cron), sigue el progreso en `/estado` y comprueba `ls data/shots`.
9. `docker inspect agregador --format '{{.HostConfig.ReadonlyRootfs}} {{.HostConfig.Memory}}'` → `true 268435456`.
10. `docker compose restart agregador` y `sudo reboot`: tras el reinicio la app vuelve sola y conserva los datos.
11. `deploy/backup.sh` genera un `.db.gz` y `gunzip -t` lo valida.
12. (HTTPS) `curl -k https://raspberrypi.local/healthz` devuelve `ok`.
13. `docker stats --no-stream`: RAM de `agregador` < 150 MB en reposo (slim).

## 13. Desinstalar

```bash
docker compose --profile shots --profile https down        # conserva ./data
docker compose --profile shots --profile https down -v     # + volúmenes (certificados de Caddy)
docker image rm ghcr.io/ivanajenjo/agregador-noticias:latest agregador-sqlite:3.22 2>/dev/null
# ¿datos también? (irreversible; haz antes un backup)
sudo rm -rf ./data ./backups
sudo systemctl disable --now agregador-backup.timer 2>/dev/null; sudo rm -f /etc/systemd/system/agregador-backup.*
```
