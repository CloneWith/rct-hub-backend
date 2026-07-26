.PHONY: verify dev test lint build run docker-up docker-down initdb initdb-seed initdb-drop generate

# Load environment variables from .env if it exists
ifneq (,$(wildcard .env))
	include .env
	export
endif

PORT ?= 8080

verify:
	go run ./tools/verify

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
	docker compose up -d --wait

docker-down:
	docker compose down

initdb:
	go run ./cmd/initdb

initdb-seed:
	go run ./cmd/initdb -seed

initdb-drop:
	go run ./cmd/initdb -drop -seed

# GraphQL 代码生成 (gqlgen)
generate:
	go run github.com/99designs/gqlgen generate
