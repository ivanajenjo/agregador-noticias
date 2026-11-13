# Funcionalidades

Todo funciona **sin LLM**; el servidor de modelos (Mac/PC) solo añade embeddings, el chat RAG, la redacción del resumen y las anotaciones de tono.
Las secciones sin datos o sin la dependencia necesaria se ocultan solas. Las etiquetas de línea editorial son **subjetivas y editables** (`config/sources.yaml`).

## Recoger
| Función | Qué hace | Dónde |
|---|---|---|
| **Ingesta RSS** | 47 medios y 81 feeds verificados (prensa, radio, TV, agencias, verificadores, regionales). GET condicional, reintentos, límite por host, dedup por URL canónica, texto completo con readability, anti-SSRF, `robots.txt` | `internal/ingest`, `config/sources.yaml` |
| **Capturas de portada** | Página completa a las 9h, 17h y 22h (configurable); Chromium **bajo demanda** (local o remoto); gestión de cookies; JPEG; retención | `internal/shot` |
| **Qué abre cada medio** | En la misma captura extrae los titulares con su posición, tamaño y prominencia (fórmula documentada) | `/portadas`, `internal/shot/frontpage.go` |
| **Titulares modificados** | Detecta cambios materiales de titular o resumen de artículos ya conocidos (ignora erratas, sufijos, reordenaciones, A→B→A) y guarda el historial | `/cambios`, `internal/revisions` |

## Buscar y preguntar
| Función | Qué hace |
|---|---|
| **Búsqueda híbrida** | FTS5 (sin tildes) + similitud coseno con fusión RRF; sin embeddings cae a solo texto |
| **Preguntar (RAG)** | Respuesta en streaming con citas `[n]`; modo *comparar medios* por línea editorial |
| **Seguimientos** | Temas guardados con sus coincidencias recientes y línea temporal |

## Entender qué está pasando
| Función | Qué hace |
|---|---|
| **Tendencias** | Términos en alza (G² de Dunning contra los 14 días previos, mínimo de medios distintos, marca «nuevo», sparkline) e **historias** agrupadas entre medios (primer medio, cobertura por línea editorial, quién no la cubre) |
| **Encuadre** | Por historia: vocabulario distintivo por bloque ideológico (log-odds con prior de Dirichlet, *Fightin' Words*) y titulares con diff de palabras frente al consenso |
| **Puntos ciegos** | Historias cubiertas casi solo por un lado del espectro; redactado como «no aparece en nuestras fuentes», sin atribuir intención |
| **Tono** | Estimaciones de tono, intención, encuadre y opinión con un modelo pequeño, por medio y línea editorial, con confianza y tamaño de muestra |
| **Medios** | Ficha por medio: volumen, mapa horario, retardo frente al primero, % de primicias, cobertura propia, términos distintivos; comparador de hasta 4 |
| **Resumen diario** | Titulares del día, historias, términos nuevos, titular más singular de cada medio y portadas; redacción opcional con LLM verificando citas; archivo y **feed Atom** |
| **Capturas en el tiempo** | Comparador antes/después con deslizador, lado a lado y mapa de diferencias; *timelapse* de la jornada; visor con zoom táctil |

## Avisar y operar
| Función | Qué hace |
|---|---|
| **Alertas** | Historias cubiertas por ≥ N medios y coincidencias de seguimientos → **ntfy, Telegram o webhook**; límite por hora, horas de silencio, dedup persistente |
| **Retención** | Borrado por lotes de artículos antiguos con limpieza de FTS, vectores y anotaciones; sin VACUUM completo (cuida la SD) |
| **Métricas y salud** | `/metrics` (Prometheus), `/healthz`, `/readyz` |
| **API** | JSON + SSE, [OpenAPI](openapi.yaml) en `/api/docs`, exportación NDJSON |
| **Móvil / PWA** | Mobile-first, navegación inferior con «Más», instalable (por HTTPS o localhost), claro/oscuro |
| **Despliegue** | Imagen multi-arch, `docker compose` con límites, HTTPS opcional con Caddy, copias de seguridad, CI a GHCR |

## Mapa de páginas
`/` Portada · `/buscar` · `/preguntar` · `/tendencias` (+ `/tendencias/termino/{key}`) · `/encuadre` (+ `/{storyID}`) · `/cambios` · `/resumen` (+ `/{día}`, `/resumen.atom`) · `/seguimientos` · `/anotaciones` · `/medios` (+ `/{id}`, `/comparar`) · `/capturas` (+ `/{medio}`) · `/portadas` · `/estado` · `/mas` · `/articulo/{id}`.
API: ver [API.md](API.md) y [openapi.yaml](openapi.yaml).

## Configuración (resumen)
Fichero `config/config.yaml` (ver `config/config.example.yaml`); cualquier clave se puede fijar con `AGREGADOR_*`.

| Bloque | Claves principales |
|---|---|
| general | `listen`, `data_dir`, `sources_file`, `timezone`, `log_level`, `auth.user/pass` |
| `ingest` | `enabled`, `cron` (30 min), `concurrency`, `per_host_delay`, `fetch_full_text`, `full_text_per_feed`, `respect_robots` |
| `screenshots` | `enabled`, `cron`, `chrome_path` / `remote_url`, `quality`, `max_height`, `retention`, `no_sandbox` |
| `embeddings` / `chat` | `base_url`, `model`, `api_key` (API compatible OpenAI), prefijos de consulta/documento |
| `retention` | `articles_days` (0 = conservar) |
| `alerts` | `enabled`, `min_sources`, `window`, `ntfy_*`, `telegram_*`, `webhook_url`, `public_url`, `max_per_hour`, `quiet_start/end` |
| `digest` | `enabled`, `cron` (08:00), `use_llm` |
| `revisions` | `enabled` |
| `annotations` | `enabled`, `base_url`, `model`, `batch_size`, `interval` |
