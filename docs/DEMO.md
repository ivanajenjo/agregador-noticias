# Datos de demostración

`cmd/demo-seed` (herramienta de desarrollo, sin red) crea un directorio de datos **ficticio y determinista** que la aplicación real puede servir tal cual.
Sirve para generar las capturas de `docs/screenshots/` y para probar la interfaz con contenido realista. Todos los sucesos, nombres, clubes y empresas son inventados.

```bash
go run ./cmd/demo-seed -out ./demo-data [-days 14] [-seed 42] [-now 2026-11-13T12:00:00Z]
AGREGADOR_DATA_DIR=./demo-data AGREGADOR_INGEST_ENABLED=false AGREGADOR_SCREENSHOTS_ENABLED=false \
AGREGADOR_DIGEST_ENABLED=false AGREGADOR_SOURCES_FILE=./demo-data/sources.yaml go run ./cmd/agregador
```

Los datos son relativos a «ahora» (las historias caen en las últimas 24 h): vuelve a generarlos antes de usarlos. Con el mismo `-seed` y el mismo `-now` el resultado es idéntico (el test comprueba el hash).

## Qué contiene
- 22 medios de `config/sources.yaml` con su línea editorial real del fichero (el generador escribe `demo-data/sources.yaml` con solo estos; la app lo carga al arrancar con `AGREGADOR_SOURCES_FILE`, para que no aparezcan columnas vacías ni «no lo cubren» con 40 medios sin datos).
- ~85 artículos/día durante `-days` días, de ruido de fondo con secciones, más:
  - 5 historias de las últimas 24 h (borrasca Ciro, huelga del transporte, decreto de ayudas, tractorada, selección) con titulares distintos según la línea, horas escalonadas y medios que no las cubren (una solo la cubren medios de izquierda y otra solo de derecha), y 6 historias más en los días anteriores;
  - 12 ediciones de titular (cambios) tras 25–70 min, términos nuevos (`Ciro`, `tractorada`, …);
  - anotaciones de tono/intención/encuadre/opinión (modelo `qwen2.5:7b`) en ~70 % de lo reciente;
  - 3 seguimientos y 7–8 alertas (una fallida y una «sin canal»), resúmenes de hoy y de los 3 días previos (generados con `internal/digest`, sin LLM);
  - 12 medios con 3 portadas sintéticas al día durante 7 días (JPEG 1366×2400 dibujados con `image/draw`; el titular principal, el diseño y los colores cambian entre capturas) y sus titulares de portada con prominencia (`/portadas`);
  - estado de feeds (4 con error) e historial de trabajos.
- Embeddings falsos de 256 dimensiones (modelo `nomic-embed-text`, bolsa de palabras con hash). Para probar la búsqueda híbrida y «Preguntar» hace falta un servidor OpenAI-compatible falso que implemente la misma función (`demo.Embed`); el script de capturas incluye uno.

Los únicos datos que no pasan por la API pública del *store* son las marcas de tiempo de `job_runs` y `watches.created_at` (el store usa la hora real): se corrigen con un `UPDATE` al final.

## Tests
`go test ./internal/demo` comprueba determinismo, historias con ≥5 medios, punto ciego de izquierda y de derecha, revisiones registradas y que el motor de tendencias real encuentre las historias y los términos nuevos.
