COMPOSE := docker compose -f deployments/docker-compose.yml

.PHONY: help up down logs test test-unit build fmt vet check

help: ## Lista os alvos disponíveis
	@grep -hE '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sort | awk 'BEGIN{FS=":.*## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

up: ## Sobe Redis e aplicação na porta 8080
	$(COMPOSE) up -d --build

down: ## Derruba tudo e apaga os dados do Redis
	$(COMPOSE) down -v

logs: ## Acompanha o log da aplicação
	$(COMPOSE) logs -f app

test: ## Roda a suíte completa no Docker, com Redis de verdade
	$(COMPOSE) run --rm test

test-verbose: ## Igual ao test, listando caso a caso
	$(COMPOSE) run --rm test go test ./... -v

test-unit: ## Roda só o que não precisa de Redis, direto na máquina
	go test ./...

build: ## Compila o binário em ./server
	go build -o server ./cmd/server

fmt: ## Formata o código
	gofmt -w ./cmd ./internal

vet: ## Analisa o código
	go vet ./...

check: fmt vet test ## Formata, analisa e roda a suíte completa
