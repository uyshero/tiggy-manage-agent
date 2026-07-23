#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
IMAGE=${TMA_PPT_SANDBOX_IMAGE:-tma-image-to-editable-ppt-sandbox:local}
TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/tma-ppt-sandbox.XXXXXX")

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

docker build \
  --file "$ROOT/deploy/image-to-editable-ppt-sandbox/Dockerfile" \
  --tag "$IMAGE" \
  "$ROOT"

docker run --rm "$IMAGE" tma-ppt-sandbox-doctor

docker image inspect "$IMAGE" --format '{{range .Config.Env}}{{println .}}{{end}}' \
  | if grep -Eq '^(OPENAI_API_KEY|OPENAI_BASE_URL|IMAGE_TO_EDITABLE_PPT_IMAGE_MODEL|TMA_PPT_VISION_MODEL|TMA_PPT_VISION_API_MODE|PADDLE_OCR_TOKEN|CODEX_AUTH_FILE)='; then
      echo "sandbox image must not contain model or OCR configuration" >&2
      exit 1
    fi

if docker run --rm "$IMAGE" sh -c 'command -v tma-ppt-vision' >/dev/null 2>&1; then
  echo "sandbox image must not contain a private vision client" >&2
  exit 1
fi

docker run --rm \
  --volume "$TMP_DIR:/workspace" \
  "$IMAGE" \
  sh -eu -c '
    python3 -c "from PIL import Image, ImageDraw; image=Image.new(\"RGB\", (1280, 720), \"white\"); draw=ImageDraw.Draw(image); draw.rectangle((80, 80, 1200, 640), fill=\"#e9eef5\"); draw.text((140, 150), \"Editable PPT smoke test\", fill=\"#111111\"); image.save(\"/workspace/source.png\")"
    editppt prepare /workspace/source.png --job-dir /workspace/run --max-concurrent-pages 1 --image-backend builtin-imagegen
    test -f /workspace/run/deck_manifest.json
    test -f /workspace/run/page_jobs.json
    test -f /workspace/run/pages/page_001/source.png
    test -f /workspace/run/pages/page_001/text_hints.json
    editppt run next /workspace/run --json >/workspace/next.json
    python3 -c "import json; data=json.load(open(\"/workspace/next.json\", encoding=\"utf-8\")); assert data[\"stage\"] == \"rebuild_page_locally\", data"
    python3 -c "import json; data=json.load(open(\"/workspace/run/deck_manifest.json\", encoding=\"utf-8\")); backend=data[\"image_backend\"]; assert backend[\"backend_id\"] == \"builtin-imagegen\" and backend[\"requires_openai_api_key\"] is False, backend"
    python3 -c "import json; data=json.load(open(\"/workspace/run/pages/page_001/text_hints.json\", encoding=\"utf-8\")); assert data[\"backend\"] == \"builtin-ink\", data"
  '

echo "image-to-editable-ppt sandbox verification passed"
