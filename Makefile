# Root automation engine

.PHONY: up down db-migrate db-seed db-reset ps

# Spin up all background docker infrastructure
up:
	docker compose up -d

# Nuke infrastructure and wipe local volumes
down:
	docker compose down -v

# Show status of local container fleet
ps:
	docker compose ps

# Run the API Gateway locally with Air hot reloading
dev-api:
	cd apps/api-gateway && air

# Run the Compliance Engine locally with Air hot reloading
dev-compliance:
	cd apps/compliance-engine && air

# Run the Broadcast Worker locally with Air hot reloading
dev-broadcast:
	cd apps/broadcast-worker && air

# Run database migrations to construct the schema
db-migrate:
	docker exec -i omnipulse-postgres psql -U admin -d omnipulse_dev < ./infra/postgres/migrations/000001_init_schema.up.sql
	docker exec -i omnipulse-postgres psql -U admin -d omnipulse_dev < ./infra/postgres/migrations/000002_add_delivery_audit.up.sql
	docker exec -i omnipulse-postgres psql -U admin -d omnipulse_dev < ./infra/postgres/migrations/000003_create_identity_and_channels.up.sql
	docker exec -i omnipulse-postgres psql -U admin -d omnipulse_dev < ./infra/postgres/migrations/000004_add_telegram_destinations.up.sql

# Reset database completely
db-reset: down up
	@echo "Waiting for postgres to start..."
	@powershell -Command "Start-Sleep -s 5"
	$(MAKE) db-migrate
	$(MAKE) db-seed

# Manual trigger to execute seed data against the local PG instance
db-seed:
	docker exec -i omnipulse-postgres psql -U admin -d omnipulse_dev < ./infra/postgres/seeds/000001_dev_contacts.sql

# Wire up local git hooks from version-controlled assets
setup-hooks:
ifeq ($(OS),Windows_NT)
	cmd /c copy "infra\git\pre-commit.sh" ".git\hooks\pre-commit"
else
	cp ./infra/git/pre-commit.sh .git/hooks/pre-commit
	chmod +x .git/hooks/pre-commit
endif
