.PHONY: up down run run-worker test test-short test-docker fmt vet seed-large build-web app-up app-down obs-up obs-down loadtest-setup loadtest

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

# database target k6 dibuat ulang dari awal; ROWS mengubah jumlah baris (bawaan 200000)
loadtest-setup:
	docker compose exec -T targetdb psql -U postgres -d sampledb -q $(if $(ROWS),-v rows=$(ROWS)) -f - < loadtest/targets.sql

loadtest:
	docker compose --profile loadtest run --rm k6
