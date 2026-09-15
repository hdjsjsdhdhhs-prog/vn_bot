# Deployment Guide

## Production architecture

The application is a single Go process with Telegram long polling, an HTTP server, background workers, and a local SQLite database. The supported VPS deployment uses the multi-stage `Dockerfile` and `docker-compose.yml`.

- Container port: `8880`
- Persistent state: `./data:/app/data`
- Configuration: `./.env:/app/.env:ro`
- Liveness: `GET /healthz`
- Readiness: `GET /readyz`
- Public routes normally proxied by nginx: `/sub/`, `/i/`, and `/payment/callback`
- Metrics: `/metrics`

Only one bot instance may use Telegram long polling for a token. Do not horizontally scale the current deployment without redesigning polling, storage, and caches.

## Image policy

Release images are produced by `.github/workflows/docker.yml` after a SemVer tag passes CI. Prefer an immutable SemVer or commit-SHA tag on production VPS hosts. `latest` is convenient but is not reproducible.

This repository is named `hdjsjsdhdhhs-prog/vn_bot`, while some inherited files still reference `kereal/rs8kvn_bot`. Verify the intended GHCR owner before deployment; otherwise Compose may pull the upstream image instead of an image built from this repository.

## Prerequisites

- Docker Engine with Compose v2
- A writable `data/` directory owned by the container user (UID/GID 1000)
- A production `.env` created from `.env.example`
- HTTPS reverse proxy and firewall
- Reachability from the VPS to configured VPN nodes
- Off-host encrypted backups

Required configuration includes `TELEGRAM_BOT_TOKEN`, positive `TELEGRAM_ADMIN_ID`, and `GLOBAL_SUB_URL`. Payment credentials are required only when payments are enabled. Restrict `.env` permissions to `600`.

## Pre-deployment checks

```bash
go mod download
gofmt -l $(find . -type f -name '*.go' -not -path './.git/*')
go test -race -count=1 -timeout 5m ./...
go vet ./...
go build ./...
golangci-lint run ./...
gosec ./...
docker compose config
docker compose -f docker-compose.local.yml config
docker build --pull -t vn_bot:verify .
```

A non-empty `gofmt -l` result is a failure. CI is authoritative for the PR.

## VPS rollout

1. Confirm the target image tag and release notes.
2. Back up `data/rs8kvn.db`, checkpoint WAL, copy the backup off-host, and verify it is readable.
3. Pull the immutable image.
4. Run `docker compose config` on the VPS.
5. Start or update with `docker compose up -d`.
6. Wait for container health, then verify `/healthz` and `/readyz` locally.
7. Check logs for migration, Telegram, node, payment, and worker errors.
8. Perform a low-risk Telegram smoke test and a subscription read.
9. Monitor errors, latency, disk usage, and pending node synchronization.

Example verification:

```bash
docker compose ps
docker compose logs --tail=200 rs8kvn_bot
curl -fsS http://127.0.0.1:8880/healthz
curl -fsS http://127.0.0.1:8880/readyz
```

## Rollback

Application rollback means pinning the previous immutable image and restarting Compose. Database rollback is separate and may be destructive. Never downgrade the binary across an incompatible migration without reviewing the migration pair and restore plan.

If a migration partially fails:

1. stop the service;
2. preserve the failed database and WAL files;
3. inspect migration state and schema integrity;
4. restore the verified backup or perform a reviewed recovery;
5. never blindly force the migration version.

## Security and operations

- Keep port 8880 bound to loopback and expose only required routes through HTTPS.
- Apply nginx rate limiting to public subscription and invite routes.
- Keep health and metrics endpoints private or authenticated.
- Run as non-root with `no-new-privileges`.
- Rotate Telegram, VPN-node, payment, and Sentry credentials after suspected exposure.
- Monitor local backup growth; current built-in backups are not off-site.
- Review `doc/operations.md` and `doc/security.md` before production changes.
