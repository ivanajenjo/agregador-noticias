# Operación

Notas para mantener la aplicación en una Raspberry Pi (SD, poca RAM).

## Retención de artículos

Sin retención la base de datos crece sin límite. Configuración:

```yaml
retention:
  articles_days: 180   # 0 = conservar todo (por defecto)
```

Variable de entorno: `AGREGADOR_RETENTION_ARTICLES_DAYS`.

- Se borran los artículos vistos por primera vez (`first_seen_at`) hace más de N
  días. Se usa esa fecha y no la de publicación para que un artículo antiguo que
  un feed sigue listando no se borre y reingeste una y otra vez.
- El borrado se hace en lotes de 200 filas (transacciones cortas) y se propaga por
  claves foráneas y disparadores a fragmentos, embeddings, anotaciones y el índice
  FTS; la caché vectorial en memoria se limpia también.
- Después: `INSERT INTO articles_fts(articles_fts) VALUES('optimize')` (si hubo
  borrados), `PRAGMA optimize`, `PRAGMA wal_checkpoint(TRUNCATE)` y, si la BD lo
  admite, `PRAGMA incremental_vacuum` acotado a 8192 páginas (32 MiB) por
  ejecución.
- Cuándo: trabajo `maintenance` a diario (04:45 por defecto) o bajo demanda con
  `POST /api/jobs/maintenance`. `GET /api/maintenance` es la simulación (cuántos
  artículos se borrarían) sin tocar nada.
- Las capturas tienen su propia retención (`screenshots.retention`, trabajo
  `cleanup`).

### Espacio en disco y VACUUM

SQLite no devuelve a la SD el espacio de las filas borradas salvo con `VACUUM`.
Un `VACUUM` completo reescribe todo el fichero (necesita espacio libre igual al
tamaño de la BD y muchas escrituras), así que **no se ejecuta nunca de forma
automática**.

- Las bases de datos **nuevas** se crean con `auto_vacuum=INCREMENTAL`; en ellas
  el mantenimiento libera páginas poco a poco.
- Las bases **existentes** (`auto_vacuum=0`) no se tocan: el espacio libre queda
  reutilizado por nuevas inserciones (el fichero deja de crecer en cuanto se
  estabiliza la retención). Si quieres encogerlo, una vez y con la app parada:
  `sqlite3 agregador.db 'PRAGMA auto_vacuum=INCREMENTAL; VACUUM;'` (haz copia antes).

### Estimación de crecimiento

Orden de magnitud con embeddings de 768 dimensiones: ~3 KB de texto indexado
(contenido + FTS) por artículo y ~3 KB por fragmento vectorial (float32 × 768),
normalmente 2-4 fragmentos. Es decir ~10-15 KB por artículo: 50 medios con ~30
artículos al día cada uno son ~1500 artículos/día, ~15-25 MB/día (~5-8 GB/año).
Sin embeddings, ~4 KB por artículo. Las capturas (WebP, ~0,3-1 MB cada una) suelen
pesar más que la BD: ajusta `screenshots.retention`. Comprueba tus cifras reales
con `agregador_db_bytes` y `agregador_shots_bytes`.

### Desgaste de la SD

- WAL con `synchronous=NORMAL` y `journal_size_limit` de 32 MiB ya reducen los fsync.
- No actives `VACUUM` periódico; deja que el mantenimiento incremental trabaje.
- Mueve `data/` a un SSD USB si puedes; es lo que más alarga la vida del equipo.
- Usa retención y baja la frecuencia de capturas si la SD es pequeña.
- Registros: evita volcar logs a la SD (`docker logs` con rotación).

## Métricas Prometheus

`GET /metrics` (formato de texto 0.0.4, sin dependencias) **requiere la misma
autenticación Basic** que el resto de la app (solo `/healthz` y `/readyz` están
exentos). Prueba rápida:

```sh
curl -s -u usuario:clave http://host:8080/metrics | grep '^agregador_'
```

Configuración de scraping en Prometheus:

```yaml
scrape_configs:
  - job_name: agregador
    scrape_interval: 60s
    basic_auth: { username: usuario, password: clave }
    static_configs: [{ targets: ['agregador.local:8080'] }]
```

Series principales (todas con cardinalidad acotada):

| Métrica | Tipo | Notas |
|---|---|---|
| `agregador_articles_total`, `agregador_articles_by_source{source}` | gauge | tope de 150 medios; el resto en `_other` |
| `agregador_articles_embedded`, `agregador_articles_embed_pending` | gauge | |
| `agregador_screenshots_total`, `agregador_db_bytes`, `agregador_shots_bytes` | gauge | |
| `agregador_oldest_article_timestamp_seconds`, `agregador_newest_article_timestamp_seconds` | gauge | |
| `agregador_job_last_run_timestamp_seconds{job}`, `..._last_success_timestamp_seconds`, `..._last_duration_seconds`, `..._last_ok`, `agregador_job_running` | gauge | de `job_runs` (ingest, screenshots, cleanup, maintenance, ...) |
| `agregador_feeds_total`, `agregador_feeds_failing`, `agregador_feed_errors_by_source{source}` | gauge | errores de feeds |
| `agregador_llm_up{role="chat\|embed"}` | gauge | salud del servidor de modelos |
| `agregador_scheduler_last_beat_timestamp_seconds` | gauge | latido del planificador |
| `agregador_http_requests_total{route,class}` | counter | `route` es el patrón del mux (p. ej. `GET /articulo/{id}`), `class` 2xx-5xx; lo no enrutado va a `unmatched` |
| `agregador_http_request_duration_seconds{route}` | histogram | |
| `agregador_http_requests_in_flight`, `agregador_store_up`, `agregador_scrape_duration_seconds` | gauge | |
| `go_goroutines`, `go_memstats_*`, `go_gc_*`, `process_start_time_seconds`, `agregador_build_info{version,goversion}` | | runtime |

La parte que consulta la BD se cachea 10 s.

### Alertas sugeridas

```yaml
- alert: AgregadorSinIngesta
  expr: time() - agregador_job_last_success_timestamp_seconds{job="ingest"} > 3 * 3600
- alert: AgregadorFeedsCaidos
  expr: agregador_feeds_failing / agregador_feeds_total > 0.3
- alert: AgregadorEmbeddingsAtrasados
  expr: agregador_articles_embed_pending > 2000
- alert: AgregadorLLMCaido
  expr: agregador_llm_up == 0
  for: 30m
- alert: AgregadorMantenimientoFalla
  expr: agregador_job_last_ok{job="maintenance"} == 0
- alert: AgregadorBDGrande
  expr: agregador_db_bytes > 8e9
```

Sin Prometheus se puede usar `curl` + `grep`/`awk` desde cron, por ejemplo para
avisar si no hay ingesta reciente:

```sh
curl -s -u u:p http://host:8080/metrics | awk '/^agregador_job_last_success_timestamp_seconds\{job="ingest"\}/ {exit (systime()-$2 > 10800)}'
```

## Salud

- `/healthz`: liveness trivial, sin autenticación (Docker `HEALTHCHECK`).
- `/readyz`: sin autenticación; JSON `{"status","checks":{db_read,db_write,scheduler}}`.
  `503` si falla la lectura, la escritura (no se puede tomar el bloqueo de
  escritura en 3 s) o si el planificador lleva más de 2 min sin latido.

## Exportación y copia de seguridad

- Análisis personal: `GET /api/export/articles.ndjson` (ver [API.md](API.md)); solo
  metadatos, no es una copia de seguridad.
- Copia de seguridad real: con la app en marcha, `sqlite3 data/agregador.db ".backup 'copia.db'"`
  (copia consistente, compatible con WAL) o parar el contenedor y copiar
  `agregador.db` (y, si existen, `-wal` y `-shm`). Las capturas están en `data/shots/`
  (opcionales: son prescindibles). La configuración está en `config/`.
- **Restaurar**: parar la app, sustituir `data/agregador.db` por la copia, borrar
  `agregador.db-wal` y `agregador.db-shm` antiguos, arrancar. Las migraciones se
  aplican solas si la copia es de una versión anterior. La caché vectorial se
  reconstruye al primer uso.
