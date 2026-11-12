VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
IMAGE   ?= ghcr.io/ivanajenjo/agregador-noticias
LDFLAGS := -s -w -X main.version=$(VERSION)-$(COMMIT)

.PHONY: build test run arm64 docker docker-build docker-build-chromium docker-buildx-arm64 \
        up down logs ps backup update release lint-deploy help

help: ## Lista los objetivos
	@grep -E '^[a-z0-9-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-22s %s\n", $$1, $$2}'

build: ## Compila el binario local
	go build -ldflags "$(LDFLAGS)" -o agregador ./cmd/agregador
test: ## go vet + tests con -race
	go vet ./... && go test -race ./...
run: build ## Ejecuta en local
	./agregador
arm64: ## Binario estatico linux/arm64 en dist/
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/agregador-arm64 ./cmd/agregador

docker: docker-build
docker-build: ## Imagen slim local (plataforma del host)
	docker build -f deploy/Dockerfile --target slim --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t agregador:latest .
docker-build-chromium: ## Imagen local con Chromium integrado
	docker build -f deploy/Dockerfile --target chromium --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t agregador:latest-chromium .
docker-buildx-arm64: ## Imagen slim linux/arm64 (en x86 requiere QEMU/binfmt)
	docker buildx build --platform linux/arm64 -f deploy/Dockerfile --target slim \
	  --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t agregador:latest-arm64 --load .

up: ## docker compose up -d (perfiles segun COMPOSE_PROFILES en .env)
	docker compose up -d
down: ## docker compose down
	docker compose down
logs: ## Sigue los logs de la app
	docker compose logs -f --tail 100 agregador
ps: ## Estado de los contenedores
	docker compose ps
backup: ## Copia SQLite consistente en ./backups (KEEP=7)
	sh deploy/backup.sh
update: ## Descarga la ultima imagen y reinicia
	sh deploy/update.sh

release: ## Crea y sube la etiqueta VERSION=vX.Y.Z; GitHub Actions publica las imagenes
	@case "$(VERSION)" in v[0-9]*) ;; *) echo "uso: make release VERSION=v1.2.3" >&2; exit 2;; esac
	@test -z "$$(git status --porcelain)" || { echo "arbol sucio: haz commit antes" >&2; exit 1; }
	$(MAKE) test
	git tag -a $(VERSION) -m "$(VERSION)"
	git push origin $(VERSION)

lint-deploy: ## shellcheck + hadolint + validacion de compose
	shellcheck -s sh deploy/*.sh
	hadolint --ignore DL3018 --ignore DL3066 --ignore DL3025 deploy/Dockerfile
	docker compose --profile shots --profile https config -q
