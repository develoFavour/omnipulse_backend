# Root automation engine

.PHONY: up down db-migrate db-seed ps

# Spin up all background docker infrastructure
up:
	docker compose up -d

# Nuke infrastructure and wipe local volumes
down:
	docker compose down -v

# Show status of local container fleet
ps:
	docker compose ps

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