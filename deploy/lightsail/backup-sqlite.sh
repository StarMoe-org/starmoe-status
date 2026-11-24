#!/usr/bin/env bash
# Safe online SQLite WAL backup script for StarMoe Status.
# Uses `sqlite3 .backup` so reads/writes are never corrupted.
#
# Example cron (daily at 04:00 UTC, as user `starmoe`):
#   0 4 * * * /opt/starmoe-status/backup-sqlite.sh >> /var/log/starmoe-status-backup.log 2>&1
#
# Optional rclone upload to Cloudflare R2 / S3:
#   Set RCLONE_DEST (e.g. "r2:starmoe-status-backups") in the environment.

set -euo pipefail

DB_PATH="${DB_PATH:-/var/lib/starmoe-status/status.db}"
BACKUP_DIR="${BACKUP_DIR:-/var/lib/starmoe-status/backups}"
RETENTION_DAYS="${RETENTION_DAYS:-30}"
RCLONE_DEST="${RCLONE_DEST:-}"

if [[ ! -f "$DB_PATH" ]]; then
  echo "[backup] database not found at $DB_PATH" >&2
  exit 1
fi

mkdir -p "$BACKUP_DIR"

TS="$(date -u +%Y%m%dT%H%M%SZ)"
TMP_DB="$(mktemp "${BACKUP_DIR}/status-${TS}.XXXXXX.db")"
OUT_GZ="${BACKUP_DIR}/status-${TS}.db.gz"

cleanup() {
  rm -f "$TMP_DB"
}
trap cleanup EXIT

sqlite3 "$DB_PATH" ".timeout 10000" ".backup '${TMP_DB}'"
gzip -c "$TMP_DB" > "$OUT_GZ"
chmod 0640 "$OUT_GZ"

echo "[backup] wrote ${OUT_GZ} ($(du -h "$OUT_GZ" | awk '{print $1}'))"

if [[ -n "$RCLONE_DEST" ]]; then
  if command -v rclone >/dev/null 2>&1; then
    rclone copy "$OUT_GZ" "$RCLONE_DEST" --no-traverse
    rclone delete "$RCLONE_DEST" --min-age "${RETENTION_DAYS}d" || true
    echo "[backup] uploaded to ${RCLONE_DEST}"
  else
    echo "[backup] RCLONE_DEST is set but rclone is not installed" >&2
    exit 1
  fi
fi

# Prune local backups older than RETENTION_DAYS
find "$BACKUP_DIR" -maxdepth 1 -type f -name 'status-*.db.gz' -mtime "+${RETENTION_DAYS}" -delete
