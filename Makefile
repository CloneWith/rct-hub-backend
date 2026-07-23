.PHONY: dev test lint build run docker-up docker-down initdb initdb-seed

# Load environment variables from .env if it exists
ifneq (,$(wildcard .env))
	include .env
	export
endif

PORT ?= 8080

build:
	go build -o ./bin/server ./cmd/server

run:
	go run ./cmd/server

dev: docker-up
	go run ./cmd/server

test:
	go test ./...

lint:
	go vet ./...
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; fi

docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

initdb:
	go run ./cmd/initdb

initdb-seed:
	go run ./cmd/initdb -seed

initdb-drop:
	go run ./cmd/initdb -drop -seed
