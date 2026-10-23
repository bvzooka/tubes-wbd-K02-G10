.PHONY: up down run test test-short fmt vet

up:
	docker compose up -d appdb targetdb testdb

down:
	docker compose down

run:
	go run ./cmd/server

test-short:
	go test -short ./...

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...
