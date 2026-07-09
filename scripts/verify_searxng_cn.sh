#!/bin/sh
set -eu

SEARXNG_URL="${TMA_WEB_SEARXNG_BASE_URL:-http://localhost:8180}"
WAIT_SECONDS="${TMA_VERIFY_SEARXNG_WAIT_SECONDS:-45}"

wait_for_http() {
  url="$1"
  label="$2"
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  while [ "$(date +%s)" -le "$deadline" ]; do
    if python3 - "$url" <<'PY' >/dev/null 2>&1
import sys
import urllib.request

with urllib.request.urlopen(sys.argv[1], timeout=3) as response:
    if 200 <= response.status < 500:
        raise SystemExit(0)
raise SystemExit(1)
PY
    then
      echo "$label is reachable"
      return 0
    fi
    sleep 1
  done
  echo "$label did not become reachable within ${WAIT_SECONDS}s: $url" >&2
  return 1
}

probe_search() {
  label="$1"
  path="$2"
  echo
  echo "== $label =="
  python3 - "$SEARXNG_URL$path" <<'PY'
import json
import sys
import urllib.request
from collections import Counter

blocked = {
    "google",
    "duckduckgo",
    "brave",
    "startpage",
    "youtube",
}
allowed_hint = {
    "baidu",
    "sogou",
    "360search",
    "quark",
    "baidu images",
    "baidu kaifa",
    "sogou images",
    "sogou videos",
    "sogou wechat",
    "bilibili",
    "bing",
    "bing images",
    "bing news",
    "bing videos",
    "wikipedia",
    "wikidata",
    "github",
    "arxiv",
}

with urllib.request.urlopen(sys.argv[1], timeout=30) as response:
    payload = json.load(response)

results = payload.get("results", [])
engines = Counter()
for result in results:
    values = result.get("engines") or [result.get("engine", "")]
    for value in values:
        if value:
            engines[value] += 1

unresponsive = payload.get("unresponsive_engines", [])
unresponsive_names = {item[0] for item in unresponsive if item}
loaded_blocked = (set(engines) | unresponsive_names) & blocked

print("query:", payload.get("query"))
print("result_count:", len(results))
print("engines:", ", ".join(f"{name}:{count}" for name, count in engines.most_common()) or "-")
print("unresponsive:", ", ".join(f"{item[0]}:{item[1]}" for item in unresponsive if len(item) >= 2) or "-")

if loaded_blocked:
    print("blocked engines still active:", ", ".join(sorted(loaded_blocked)), file=sys.stderr)
    raise SystemExit(2)

if not (set(engines) | unresponsive_names) & allowed_hint:
    print("no expected CN/knowledge engines appeared in response", file=sys.stderr)
    raise SystemExit(3)
PY
}

echo "Recreating SearXNG container"
docker compose up -d --force-recreate searxng

wait_for_http "$SEARXNG_URL/healthz" "SearXNG"

probe_search "中文综合搜索" "/search?q=%E6%B5%8B%E8%AF%95&format=json"
probe_search "Bing 国内节点" "/search?q=harness&format=json&engines=bing"
probe_search "中文图片垂类" "/search?q=%E7%8C%AB&format=json&categories=images"
probe_search "中文视频垂类" "/search?q=%E5%91%A8%E6%9D%B0%E4%BC%A6&format=json&categories=videos"
probe_search "知识/开发垂类" "/search?q=golang&format=json&engines=github,arxiv,wikipedia,wikidata"

echo
echo "SearXNG CN verification passed"
