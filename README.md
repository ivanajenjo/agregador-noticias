# Agregador de noticias

Agrega noticias de medios españoles de distintas líneas editoriales, guarda capturas periódicas de sus portadas,
las indexa (texto completo + vectores) y permite **buscar y preguntar (RAG)** para comparar cómo cuenta cada medio lo mismo.
Un único binario Go (sin CGO) pensado para una Raspberry Pi compartida; el LLM corre en otra máquina.

![Portada](docs/screenshots/portada-desktop-light.png)

- Requisitos: [docs/REQUISITOS.md](docs/REQUISITOS.md) · Arquitectura: [docs/ARQUITECTURA.md](docs/ARQUITECTURA.md)
- Más capturas (escritorio/móvil, claro/oscuro): [docs/screenshots/](docs/screenshots/)

## Arranque rápido
```bash
go run ./cmd/agregador            # http://localhost:8080 (datos en ./data)
```
Configuración: copia `config/config.example.yaml` a `config/config.yaml`; cualquier valor se puede sobreescribir con `AGREGADOR_*`.
Medios y feeds: `config/sources.yaml`.

## Conectar el LLM del Mac/PC
Cualquier servidor con API OpenAI-compatible (Ollama, LM Studio, llama.cpp, mlx-lm):
```bash
AGREGADOR_EMBEDDINGS_BASE_URL=http://macbook.local:11434/v1 AGREGADOR_EMBEDDINGS_MODEL=nomic-embed-text \
AGREGADOR_CHAT_BASE_URL=http://macbook.local:11434/v1       AGREGADOR_CHAT_MODEL=qwen3:14b \
go run ./cmd/agregador
```
Si el Mac está apagado, la ingesta sigue y la búsqueda funciona con FTS; los embeddings se completan solos al volver.

## Despliegue en la Raspberry
Imagen Docker multi-arch (arm64/amd64) publicada en GHCR desde GitHub Actions; compose con límites de RAM/CPU, healthcheck,
sistema de ficheros de solo lectura y logs rotados, pensado para una Pi **compartida**. Guía completa: [docs/DESPLIEGUE.md](docs/DESPLIEGUE.md).
```bash
git clone https://github.com/ivanajenjo/agregador-noticias.git && cd agregador-noticias
deploy/pi-install.sh --install-docker     # .env, imagen, `docker compose up -d` y URL (opciones: --chromium --shots --https)
```
- Variables: [.env.example](.env.example) (puerto, auth, LLM del Mac, capturas, límites). Usa la **IP** del Mac, no `macbook.local`.
- Capturas: imagen `-chromium` (bajo demanda), sidecar `--profile shots` o Chromium remoto. HTTPS para la PWA: `--profile https` (Caddy).
- Copias: `make backup` · Actualizar: `make update` · Binario suelto: `make arm64` (estático, ~17 MB) · Release: `make release VERSION=v1.0.0`.

## Estado y siguientes pasos
Hecho: ingesta RSS + texto completo, capturas, FTS5 + búsqueda vectorial híbrida, RAG con comparación por línea editorial, UI móvil/PWA.
Previsto: anotación de sesgo/intención por noticia con un modelo pequeño (esquema `article_annotations` ya creado), cuantización int8 de vectores.
Notas: Público, La Razón, SER, RTVE y otros no tenían RSS válido al verificar (ver `config/sources.yaml`); las etiquetas de línea editorial son subjetivas y editables.
