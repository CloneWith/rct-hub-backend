.PHONY: verify dev test test-integration lint build run docker-up docker-down initdb initdb-seed initdb-drop initdb-admin generate fixtures matchmock graphql-compat

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
	$(MAKE) initdb
	go run ./cmd/server

test:
	go test ./...

test-integration:
	@test -n "$(MONGODB_TEST_URI)" || (echo "MONGODB_TEST_URI is required" && exit 1)
	go test -race -count=1 -run '^TestMongoIntegration' -v ./internal/persistence

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

# 初始化数据库并创建管理员用户
# 用法: make initdb-admin ADMIN_ID=12345 ADMIN_NAME=myadmin
initdb-admin:
	go run ./cmd/initdb -admin-id=$(ADMIN_ID) -admin-name=$(ADMIN_NAME)

# GraphQL 代码生成 (gqlgen)
generate:
	go run github.com/99designs/gqlgen generate

fixtures:
	go run ./tools/matchfixtures

matchmock:
	go run ./tools/matchmock

graphql-compat:
	go run ./tools/graphqlcompat
