#!/bin/sh
set -eu

# Biography is an independent project. Keep these legacy source and migration
# paths out of Platform so the two projects cannot silently diverge.
legacy_paths='apps/biography-mobile
cmd/tma-biography-agent-bootstrap
cmd/tma-biography-voice-gateway
internal/biographyvoice
skills/conduct-biography-interview
skills/structure-biography-chapters
skills/verify-biography-facts
skills/embed.go
docs/biography-voice-gateway.md
docs/biography-voice-production.md
scripts/keycloak_biography_client.sh
sql/migrations/000103_biography_voice_persistence.sql
sql/migrations/000104_biography_recording_segments.sql'

printf '%s\n' "$legacy_paths" | while IFS= read -r legacy_path; do
  if [ -e "$legacy_path" ]; then
    echo "Biography boundary violation: legacy path still exists: $legacy_path" >&2
    exit 1
  fi
done

echo "Repository boundary check passed"
