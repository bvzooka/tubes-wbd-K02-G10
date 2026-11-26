.PHONY: up down run run-worker test test-short test-docker fmt vet seed-large build-web app-up app-down obs-up obs-down

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

build-web:
	cd frontend && npm ci && npm run build

app-up:
	docker compose --profile app up --build -d server worker

app-down:
	docker compose --profile app down

obs-up:
	docker compose --profile observability up -d otel-collector tempo loki prometheus grafana

# hanya menghentikan stack observability; appdb dan targetdb tetap berjalan
obs-down:
	docker compose --profile observability rm -sf otel-collector tempo loki prometheus grafana

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
