.PHONY: help build run test lint migrate-up migrate-down migrate-status docker-build docker-up docker-down

help:
	@echo "Moistello Backend - Available Commands:"
	@echo "  make build        - Build API server binary"
	@echo "  make run          - Run API server"
	@echo "  make test         - Run all tests"
	@echo "  make lint         - Run linter"
	@echo "  make migrate-up     - Apply all pending migrations"
	@echo "  make migrate-down   - Rollback the last migration (STEPS=n for more)"
	@echo "  make migrate-status - Show applied and pending migrations"
	@echo "  make docker-up    - Start Docker Compose services"
	@echo "  make docker-down  - Stop Docker Compose services"
	@echo "  make docker-build - Build Docker images"
	@echo "  make seed         - Seed database with dev data"

build:
	go build -o bin/moistello-api ./cmd/api-server

run:
	go run ./cmd/api-server

run-indexer:
	go run ./cmd/indexer

test:
	go test ./... -count=1

test-cover:
	go test ./... -coverprofile=coverage.out && go tool cover -html=coverage.out -o coverage.html

lint:
	golangci-lint run ./...

migrate-up:
	go run ./cmd/migrate --direction up

migrate-down:
	go run ./cmd/migrate --direction down --steps $(or $(STEPS),1)

migrate-status:
	go run ./cmd/migrate --direction status

seed:
	go run ./scripts/seed.go

docker-build:
	docker build -t moistello-api:latest .
	docker build -f Dockerfile.indexer -t moistello-indexer:latest .

docker-up:
	docker compose up -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f
