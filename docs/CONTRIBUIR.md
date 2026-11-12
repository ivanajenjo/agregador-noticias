# Guía de desarrollo y extensión

## Principios
Go puro (sin CGO), poco consumo (Raspberry Pi compartida), funciona **sin LLM** y degrada bien si el servidor de modelos está apagado.
Los paquetes dependen solo de `internal/domain` (contratos); el cableado vive en `cmd/agregador/main.go`.

## Cómo añadir una funcionalidad (puntos de extensión)
| Qué | Dónde |
|---|---|
| Contrato (tipos/interfaz) | **fichero nuevo** `internal/domain/<feature>.go`; solo cambios aditivos en los existentes |
| Esquema SQLite | **fichero nuevo** `internal/store/migrations_<feature>.go` con un `init()` que asigna `migrations[N-1]` en su hueco reservado (3 revisiones, 4 seguimientos/alertas, 5 resúmenes, 6 libre). Nunca editar una migración ya aplicada |
| Consultas | ficheros nuevos `internal/store/<feature>.go` (+ test) |
| Lógica | paquete propio `internal/<feature>/` |
| Web | ficheros nuevos `internal/web/<feature>.go`, `templates/<feature>.html`; en un `init()`: `registerPage("<nombre>")`, `registerRoutes(func(s *server, m *http.ServeMux){...})`, `registerNav(NavItem{...})` (los no `Primary` salen en «Más» en móvil). Dependencias nuevas: un campo en `web.Deps` (nil ⇒ la sección se oculta/503) |
| CSS | fichero propio `static/<feature>.css` enlazado desde el bloque `{{define "head"}}` de su plantilla; JS propio `static/<feature>.js` (sin scripts inline: CSP `script-src 'self'`) |
| Config | las secciones ya existen en `internal/config/config.go` (`alerts`, `digest`, `revisions`, `annotations`, `retention`); añadir campos nuevos de forma aditiva + `AGREGADOR_*` |
| Trabajos periódicos | exponer `RunOnce(ctx) (string, error)`; se registran en `cmd/agregador/main.go` vía `sched.Job` |

## Reglas
- Texto de la UI en español; identificadores en inglés. HTML escapado siempre (los titulares son datos no confiables).
- Diseño: tokens CSS existentes (`--bg --surface --text --muted --border --accent`, serif para titulares), claro/oscuro, móvil primero (390 px), áreas táctiles ≥ 44 px, sin dependencias externas en ejecución.
- Tests sin red (`httptest`, fakes); `go vet` y `go test -race` en verde; `gofmt`.
- No ejecutar `go mod tidy` en paralelo con otros; no hacer commit (lo hace el coordinador).
- Las inferencias de modelos (tono, intención, sesgo) se muestran como **estimaciones** con el modelo usado, no como hechos.
