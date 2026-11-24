.PHONY: up down run run-worker test test-short test-docker fmt vet seed-large

up:
	docker compose up -d appdb targetdb testdb

down:
	docker compose down


seed-large:
	docker compose exec -T targetdb psql -U postgres -d sampledb -v ON_ERROR_STOP=1 -1 $(if $(ROWS),-v rows=$(ROWS)) -f - < docker/targetdb/large.sql

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
