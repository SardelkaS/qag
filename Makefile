.DEFAULT_GOAL := help
export QAG_HTTP_PORT ?= 8080

.PHONY: help init build up down restart logs ps import-local
help:
	@echo "init: prepare private configuration and PKI"
	@echo "build: build application and setup images"
	@echo "up: build and start qag with PostgreSQL; wait for readiness"
	@echo "down: stop services; preserve volumes"
	@echo "restart: restart qag and PostgreSQL"
	@echo "logs: follow qag and PostgreSQL logs"
	@echo "ps: show service state"
	@echo "import-local: import existing .local configuration (stop native qag first)"
	@echo "QAG_HTTP_PORT=8080 (override with make up QAG_HTTP_PORT=18088)"
init:
	docker compose run --build --rm --no-deps init
build:
	docker compose build
up:
	docker compose up --build -d --wait
down:
	docker compose down
restart:
	docker compose restart qag postgres
logs:
	docker compose logs -f qag postgres
ps:
	docker compose ps -a
import-local:
	docker compose --profile tools run --build --rm --no-deps import-local
