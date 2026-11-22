.PHONY: up down run run-worker test test-short test-docker fmt vet

up:
	docker compose up -d appdb targetdb testdb

down:
	docker compose down

run:
	go run ./cmd/server

run-worker:
	go run ./cmd/worker

test-short:
	go test -short ./...

test:
	go test ./...

test-docker:
	docker compose run --rm test

fmt:
	gofmt -w .

vet:
	go vet ./...
