# Agregador de noticias — Requisitos (v2, reinicio en Go)

## 1. Objetivo
Recopilar noticias de medios españoles de distinta línea editorial, conservar capturas
periódicas de sus portadas, indexarlo todo en una base vectorial y permitir **buscar y
preguntar (RAG)** para comparar cómo cada medio cuenta lo mismo. Uso personal, en LAN.

## 2. Restricciones de despliegue (condicionan todo)
- Se despliega en una **Raspberry Pi (arm64) compartida con otros proyectos** → poco CPU/RAM/disco/SD-wear.
  - Objetivo: **< 100 MB RSS en reposo**, un único binario Go estático, sin JVM/Node/Python.
  - Sin servicios pesados permanentes (nada de Elasticsearch/Qdrant/Postgres/Chromium siempre encendido).
  - Escrituras a disco acotadas (SQLite WAL, capturas JPEG comprimidas, retención configurable).
- **El LLM y los embeddings NO corren en la Pi.** Corren en otra máquina (MacBook/PC) y se consumen
  por HTTP con API compatible OpenAI (`/v1/embeddings`, `/v1/chat/completions`): Ollama, LM Studio,
  llama.cpp server, mlx-lm, vLLM… La máquina remota **puede estar apagada**: el sistema debe degradarse bien.
- Cross-compilación trivial a `linux/arm64` (Go puro, **sin CGO**) e imagen Docker multi-arch.

## 3. Requisitos funcionales
### RF1 Ingesta de noticias
- Fuentes definidas en `config/sources.yaml` (id, nombre, línea editorial, feeds RSS/Atom, portada).
- Alta inicial de ≥ 10 medios de espectro variado; añadir un medio = editar YAML, sin tocar código.
- Descarga periódica (por defecto cada 30 min) con GET condicional (ETag/Last-Modified), timeouts,
  reintentos con backoff, límite de concurrencia y de peticiones por host, User-Agent identificable.
- Extracción de texto completo del artículo (readability) cuando el feed solo trae resumen; configurable por fuente.
- Deduplicación por URL canónica (sin parámetros de tracking) y por hash de título normalizado por fuente.
- Se conserva: titular, resumen, texto, autor, sección, fecha de publicación, primera vez visto, URL.
- Un fallo en un medio nunca afecta a los demás; el estado por feed (último OK, último error) es visible.

### RF2 Capturas de portada
- Captura de página completa de la portada de cada medio a horas configurables (p. ej. 09:00, 17:00, 22:00, zona Europe/Madrid).
- Gestión de banner de cookies (selectores configurables por medio + heurística genérica); si falla, se guarda igual y se registra.
- Navegador headless **bajo demanda**: o bien Chromium local lanzado solo durante la captura, o bien un
  Chromium remoto (CDP/websocket, p. ej. en el Mac o un contenedor `chromedp/headless-shell`). Nunca residente en la Pi por defecto.
- Capturas secuenciales (una a la vez), JPEG con calidad configurable, almacenadas en disco
  `data/shots/<medio>/<YYYY>/<MM>/<DD>/<HHMM>.jpg`, metadatos en BD.
- Retención configurable (p. ej. 90 días) con limpieza automática.
- Disparo manual desde la UI/API.

### RF3 Índice vectorial y búsqueda
- Cada artículo se trocea si es largo y se embebe; los vectores se guardan junto a la BD (SQLite, BLOB float32) con
  el **nombre del modelo** (cambiar de modelo = reindexar automáticamente, sin perder datos).
- Cola de embeddings con backfill: si el servidor de embeddings no responde, se acumula y se procesa cuando vuelve.
- Búsqueda **híbrida**: FTS5 (palabras clave, español) + similitud coseno, fusionadas con RRF.
  Sin embeddings disponibles → solo FTS5 (degradación elegante).
- Filtros: medio, línea editorial, rango de fechas.
- Detrás de una interfaz `VectorIndex` para poder cambiar a sqlite-vec/Qdrant si el volumen crece.

### RF4 RAG
- Pregunta en lenguaje natural → recuperación híbrida → prompt con fragmentos numerados → LLM remoto con streaming (SSE) → respuesta con citas `[n]` enlazadas a los artículos.
- Modo "comparar": misma consulta recuperando por separado por línea editorial/medio y pidiendo al modelo un contraste de enfoques (titulares, énfasis, omisiones) citando fuentes.
- Si el LLM no está disponible: mensaje claro y se muestran igualmente los resultados de búsqueda.
- Endpoints de configuración: URL base, modelo de chat, modelo de embeddings, API key opcional, por separado.

### RF5 Interfaz web (Go templates + htmx, sin build de JS)
- **Portada**: últimas noticias en columnas por medio/línea editorial; auto-refresco htmx.
- **Búsqueda**: caja con resultados híbridos, filtros, paginación.
- **Preguntar**: chat RAG con streaming y citas.
- **Capturas**: elegir día y ver portadas lado a lado; línea temporal por medio; ampliar imagen.
- **Estado**: salud de fuentes, última ingesta, cola de embeddings, estado del LLM remoto, espacio en disco.
- Español, sin dependencias externas en runtime (htmx vendorizado y embebido en el binario).
- **Móvil de primera clase** (se usará mucho desde el teléfono): diseño mobile-first, **PWA instalable**
  (manifest + service worker mínimo con página offline), navegación inferior tipo app, áreas táctiles ≥ 44 px,
  visor de capturas a pantalla completa con zoom/swipe, chat RAG cómodo con teclado, tablas convertidas en tarjetas.

### RF6 API JSON (misma lógica que la UI)
`GET /api/articles`, `/api/search`, `/api/screenshots`, `POST /api/ask` (SSE), `POST /api/jobs/{ingest|screenshots}`, `GET /healthz`, `GET /api/status`.

## 4. Requisitos no funcionales
- **Robustez**: reintentos, timeouts en todo I/O, apagado ordenado (SIGTERM), migraciones de BD versionadas, jobs idempotentes y reanudables.
- **Observabilidad**: `log/slog` estructurado, `/healthz`, `/api/status`, tabla de ejecuciones de jobs.
- **Seguridad**: escucha en LAN; auth básica opcional (`AUTH_USER/AUTH_PASS`); sin secretos en el repo; fetch de URLs limitado a http(s) y descarte de IPs privadas en extracción de artículos (anti-SSRF); límites de tamaño de respuesta.
- **Configuración**: YAML + variables de entorno (`AGREGADOR_*`) con valores por defecto sensatos; ver `config/config.example.yaml`.
- **Calidad**: tests unitarios con `httptest` (feeds, LLM falso), `go vet`, `golangci-lint` opcional, CI GitHub Actions (test + build arm64 + imagen).
- **Mantenibilidad**: paquetes pequeños con interfaces en `internal/domain`; sin frameworks pesados (stdlib `net/http` + `chi` opcional).
- **Legal/ético**: uso personal; respetar `robots.txt` en extracción de artículo completo, rate limit, no republicar contenido. Las "líneas editoriales" son etiquetas **editables por el usuario**, no una verdad objetiva.

## 5. Etapas posteriores (el diseño debe dejarlas preparadas)
- **Anotación de sesgo/intención por noticia con un modelo pequeño** (clasificador local o LLM pequeño en el
  Mac/PC, p. ej. 1–8B vía el mismo servidor OpenAI-compatible): etiquetas como tono (neutral/alarmista/…),
  intención (informar/opinar/persuadir), encuadre o posicionamiento, con puntuación y justificación breve.
  - Tabla `article_annotations (article_id, kind, model, label, score, data JSON)`: varios modelos y tipos conviven,
    cambiar de modelo = reanotar sin perder lo anterior.
  - Worker de anotación análogo al de embeddings (backfill, tolerante a que el Mac esté apagado, por lotes, en horas de baja carga).
  - Las etiquetas son **inferencias probabilísticas del modelo, no verdades**: la UI las muestra como tales, con el modelo usado.
  - UI futura: filtros/agrupación por etiqueta, comparación de tono entre medios para un mismo tema, y uso como filtro en RAG.

## 6. Fuera de alcance (por ahora)
Multiusuario, app móvil nativa (se cubre con PWA), notificaciones, scraping de contenidos tras paywall.

## 7. Criterios de aceptación
1. `go build ./... && go test ./...` en verde; `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build` funciona.
2. Con red: tras arrancar, aparecen artículos de ≥ 8 medios en la portada en pocos minutos.
3. Con un servidor OpenAI-compatible falso, los artículos se embeben y la búsqueda híbrida y el RAG con citas funcionan; sin él, la búsqueda FTS funciona y la UI explica el estado.
4. Una captura de portada de al menos un medio se guarda, se ve en la UI y se limpia por retención.
5. RSS en reposo < 100 MB con 50k artículos indexados (brute-force coseno medido y documentado).
6. La UI es usable a 390×844 y se puede instalar como PWA; el esquema admite anotaciones (tabla + worker futuro).
7. `docker compose up` levanta el servicio; documentado cómo apuntar al LLM del Mac.
