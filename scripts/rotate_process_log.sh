#!/bin/sh
set -eu

LOG_FILE="${1:?log file path is required}"
MAX_BYTES="${2:?maximum log size is required}"
BACKUP_COUNT="${3:?backup count is required}"

case "$MAX_BYTES" in
  '' | *[!0-9]*)
    printf 'maximum log size must be a positive integer: %s\n' "$MAX_BYTES" >&2
    exit 1
    ;;
esac
case "$BACKUP_COUNT" in
  '' | *[!0-9]*)
    printf 'log backup count must be a positive integer: %s\n' "$BACKUP_COUNT" >&2
    exit 1
    ;;
esac
if [ "$MAX_BYTES" -lt 1 ] || [ "$BACKUP_COUNT" -lt 1 ]; then
  printf '%s\n' 'maximum log size and backup count must both be greater than zero' >&2
  exit 1
fi
if [ ! -f "$LOG_FILE" ]; then
  exit 0
fi

size_bytes="$(LC_ALL=C wc -c <"$LOG_FILE" | tr -d '[:space:]')"
if [ "$size_bytes" -lt "$MAX_BYTES" ]; then
  exit 0
fi

index="$BACKUP_COUNT"
while [ "$index" -gt 1 ]; do
  previous=$((index - 1))
  if [ -f "$LOG_FILE.$previous" ]; then
    mv -f "$LOG_FILE.$previous" "$LOG_FILE.$index"
  fi
  index="$previous"
done
mv -f "$LOG_FILE" "$LOG_FILE.1"
printf 'rotated log_file=%s size_bytes=%s backups=%s\n' "$LOG_FILE" "$size_bytes" "$BACKUP_COUNT"
