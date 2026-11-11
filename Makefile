DEV = docker compose -f docker-compose.yml -f docker-compose.dev.yml

.PHONY: dev up test down

# Hot reload: Go (air) + Vue (HMR) + Postgres, in the foreground.
dev:
	$(DEV) up --build

# Production-like: compiled API + nginx-served static frontend, detached.
up:
	docker compose up --build -d

# Runs inside the dev images (no local Go/Node needed); starts Postgres for the DB tests.
test:
	$(DEV) run --build --rm backend go test ./...
	$(DEV) run --build --rm --no-deps frontend npm run type-check

down:
	docker compose down --remove-orphans
