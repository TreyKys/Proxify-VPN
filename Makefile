.PHONY: help dev-db migrate server test test-server test-core test-edge fmt

help:
	@grep -E '^[a-z-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "};{printf "  %-14s %s\n", $$1, $$2}'

TEST_DB ?= postgres://proxify:proxify@localhost:5432/proxify?sslmode=disable

dev-db: ## Start the local Postgres
	docker compose -f infra/docker-compose.dev.yml up -d

migrate: ## Apply migrations
	cd server && DATABASE_URL="$(TEST_DB)" go run ./cmd/migrate

server: ## Run the control plane
	cd server && go run ./cmd/api

test: test-server test-core test-edge ## Run everything

# Provisioning correctness lives in SQL as much as in Go — partial unique
# indexes, FOR UPDATE ordering, SKIP LOCKED claims. Faking the database would
# test a different program than the one we ship, so these need a real Postgres.
test-server: ## Go control-plane tests (needs a database)
	cd server && TEST_DATABASE_URL="$(TEST_DB)" go test ./...

test-core: ## Android reliability engine tests (no SDK needed)
	cd android && gradle :core:test

test-edge: ## Edge agent tests
	cd edge && go test ./...

fmt: ## Format
	cd server && gofmt -w .
	cd edge && gofmt -w .
