# API HTTP

La especificación completa (OpenAPI 3.1) está en [`openapi.yaml`](openapi.yaml) y la
sirve la propia aplicación: `GET /api/openapi.yaml` (YAML) y `GET /api/docs`
(visor HTML sin dependencias externas).

## Autenticación

Si hay credenciales configuradas (`auth.user` / `auth.pass`), todas las rutas
exigen HTTP Basic, salvo `/healthz` y `/readyz`.

```sh
curl -u usuario:clave http://agregador.local:8080/api/status
```

Las peticiones que modifican estado (`POST`) necesitan además la cabecera
`X-Requested-With` (cualquier valor) o un `Origin` del mismo host (protección CSRF):

```sh
curl -u usuario:clave -X POST -H 'X-Requested-With: curl' http://agregador.local:8080/api/jobs/ingest
```

## Formato de error

Los errores de `/api/*` son JSON con estado HTTP adecuado:

```json
{"error": "Falta el parámetro q"}
```

Códigos habituales: `400` parámetro no válido, `401` sin credenciales, `403` CSRF,
`404` no existe, `409` el trabajo ya está en marcha, `429` exportación en curso,
`503` funcionalidad no configurada (p. ej. sin servidor de modelos).

## Ejemplos

```sh
# Últimos artículos de un medio, paginados con limit/offset
curl -u u:p 'http://host:8080/api/articles?source=elpais&limit=20&offset=20'

# Búsqueda (modo "hybrid" o "text" si no hay embeddings); paginada con page/limit
curl -u u:p 'http://host:8080/api/search?q=vivienda&from=2026-11-01&page=2'

# Tendencias y historias
curl -u u:p 'http://host:8080/api/trends?ventana=24h'
curl -u u:p 'http://host:8080/api/stories?ventana=48h'

# Preguntar: respuesta en streaming SSE (-N desactiva el búfer de curl)
curl -N -u u:p -X POST -H 'X-Requested-With: curl' -H 'Content-Type: application/json' \
  -d '{"question":"¿Cómo cubren la vivienda los distintos medios?","compare":true}' \
  http://host:8080/api/ask
```

Eventos SSE de `/api/ask`: `sources` (JSON con las fuentes recuperadas, se envía
primero), `delta` (fragmentos de texto), `error` (mensaje legible) y `done`.
Cada ~15 s se emite un comentario `: ping` como latido.

### Trabajos

```sh
curl -u u:p -X POST -H 'X-Requested-With: curl' http://host:8080/api/jobs/ingest        # 202 / 409 / 503
curl -u u:p -X POST -H 'X-Requested-With: curl' http://host:8080/api/jobs/maintenance   # retención + mantenimiento
curl -u u:p http://host:8080/api/maintenance                                           # simulación: cuántos se borrarían
```

Los trabajos lanzados desde la API/UI quedan registrados en `job_runs` igual que
los del planificador (aparecen en `/estado` y en `/metrics`).

### Exportación

```sh
curl -u u:p -o articulos.ndjson \
  'http://host:8080/api/export/articles.ndjson?from=2026-10-01&to=2026-10-31&source=elpais'
```

Una línea JSON por artículo con `title`, `url`, `source`, `published` y
`summary` (sin contenido completo). Acepta `source`, `leaning`, `from`, `to` y
`limit`.

## Paginación y límites

| Endpoint | Paginación | Límite |
|---|---|---|
| `/api/articles` | `limit` (1-100, 30), `offset` (0-100000) | |
| `/api/search` | `page`, `limit` (1-50, 10) | `q` hasta 500 caracteres |
| `/api/revisions` | `limit` (1-100, 30), `offset` | |
| `/api/ask` | | cuerpo 32 KiB, pregunta 2000 caracteres, `k` ≤ 20 |
| `/api/export/articles.ndjson` | streaming en bloques de 500 | 200 000 líneas, una exportación a la vez |

Fechas `from`/`to` en formato `AAAA-MM-DD` (zona horaria configurada; `to`
inclusivo). Todas las peticiones salvo `/api/ask` y la exportación tienen un
límite de 30 s. Las respuestas llevan `Cache-Control: no-store`.

## Operación

- `GET /healthz`: liveness trivial (`ok`), sin autenticación; lo usa Docker.
- `GET /readyz`: JSON con comprobaciones (lectura y escritura de la BD, latido del
  planificador); `200` si todo va bien, `503` si algo falla. Sin autenticación.
- `GET /metrics`: formato de texto de Prometheus, **con** autenticación.
  Ver [OPERACION.md](OPERACION.md).
