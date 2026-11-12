# Arquitectura

```
                    Raspberry Pi (arm64, compartida)                       Mac / PC potente
 ┌──────────────────────────────────────────────────────────┐        ┌───────────────────────┐
 │  agregador (1 binario Go)                                │        │ Ollama / LM Studio /  │
 │                                                          │  HTTP  │ llama.cpp / mlx-lm    │
 │  sched ──► ingest (RSS + readability) ─┐                 │ ─────► │  /v1/embeddings       │
 │        └─► shot (chromedp) ──────────┐ │                 │        │  /v1/chat/completions │
 │                                      ▼ ▼                 │        └───────────────────────┘
 │  llm.EmbedWorker (backfill) ──► store (SQLite WAL+FTS5)  │        ┌───────────────────────┐
 │  rag (hybrid search + prompt) ◄────────┘                 │  CDP   │ (opcional) Chromium   │
 │  web (net/http + html/template + htmx, SSE)              │ ─────► │ remoto para capturas  │
 └──────────────────────────────────────────────────────────┘        └───────────────────────┘
        data/ : agregador.db  +  shots/<medio>/YYYY/MM/DD/HHMM.jpg
```

## Decisiones

| Tema | Decisión | Motivo |
|---|---|---|
| Lenguaje | Go 1.26+, sin CGO | Binario estático, ~20-50 MB RAM, cross-compile arm64 trivial |
| BD | SQLite vía `modernc.org/sqlite` (puro Go) | Cero servicios extra en la Pi; WAL; FTS5 incluido |
| Vectores | BLOB float32 en SQLite + coseno por fuerza bruta en Go, caché en memoria, tras interfaz `VectorIndex` | 50k × 768 dims ≈ 150 MB en float32; con cuantización int8 en memoria ≈ 38 MB. Suficiente para hobby; evita Qdrant/pgvector. Intercambiable |
| Búsqueda | Híbrida FTS5 (BM25) + coseno, fusión RRF | Mejor recall en español y nombres propios; funciona sin LLM |
| LLM | Cliente HTTP OpenAI-compatible, URLs separadas para chat y embeddings | Cualquier runtime local; Mac apagado = degradación, no fallo |
| Capturas | `chromedp`; Chromium remoto (CDP) o local bajo demanda | No tener Chromium residente en la Pi |
| Front | `html/template` + htmx embebido + SSE | Sin build de JS, binario único |
| Scheduler | Interno (`robfig/cron/v3`, TZ Europe/Madrid) | Sin cron externo |
| Config | YAML + env `AGREGADOR_*` | Docker-friendly |
| Routing | `net/http` (Go 1.22 patterns) | Sin dependencias |

## Estructura

```
cmd/agregador/          main: wiring, señales, apagado ordenado
internal/domain/        tipos y INTERFACES (contrato entre paquetes)
internal/config/        carga YAML+env, validación
internal/store/         SQLite: migraciones, artículos, FTS5, vectores, capturas, estado de jobs
internal/ingest/        fetch feeds, readability, normalización, dedup  → domain.Store
internal/shot/          capturas con chromedp, cookies, retención
internal/llm/           cliente OpenAI-compat (Embedder, Chat stream), EmbedWorker, health
internal/rag/           chunking, búsqueda híbrida (RRF), prompts, comparador
internal/web/           handlers, templates, static (htmx), SSE, auth básica
internal/sched/         cron jobs
config/                 sources.yaml, config.example.yaml
deploy/                 Dockerfile, compose, systemd, docs de despliegue
docs/                   requisitos, arquitectura
```

## Reglas para trabajar en paralelo
- Los contratos viven en `internal/domain`. **No se modifican sin avisar**; si hace falta algo nuevo, se añade (no se rompe).
- Cada paquete depende solo de `domain` (y de la stdlib/libs externas), nunca de otro paquete interno hermano. El wiring está en `cmd/agregador`.
- Los tests no requieren red: `httptest` y fakes.
