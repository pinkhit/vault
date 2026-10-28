#!/bin/sh
# Adds one synthetic document and leaves the service running for inspection.
# Does not remove containers or volumes on failure, so logs remain available.
set -eu
cd "$(dirname "$0")/.."

docker compose config --quiet
docker compose up --build -d --wait

port="$(docker compose port app 8080)"
case "$port" in
    127.0.0.1:*) ;;
    *)
        printf 'Refusing non-loopback mapping: %s\n' "$port" >&2
        exit 1
        ;;
esac

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
base_url="http://$port"
fixture="tests/fixtures/fictional-invoice.txt"

curl -fsS "$base_url/" -o "$work_dir/index.html"
python3 - "$work_dir/index.html" <<'PY'
from pathlib import Path
import sys

assert "FamilyVault" in Path(sys.argv[1]).read_text()
PY

checksum="$(python3 - "$fixture" <<'PY'
import hashlib
from pathlib import Path
import sys

print(hashlib.sha256(Path(sys.argv[1]).read_bytes()).hexdigest())
PY
)"

curl -fsS \
    -H "X-Checksum-SHA256: $checksum" \
    -F "file=@$fixture" \
    "$base_url/api/documents" \
    -o "$work_dir/upload.json"

document_id="$(python3 - "$work_dir/upload.json" "$checksum" <<'PY'
import json
from pathlib import Path
import sys

document = json.loads(Path(sys.argv[1]).read_text())
assert document["sha256"] == sys.argv[2]
print(document["id"])
PY
)"

docker compose restart app
docker compose up -d --wait

curl -fsS "$base_url/api/documents/$document_id/download" \
    -o "$work_dir/download.txt"
cmp "$fixture" "$work_dir/download.txt"

curl -fsS "$base_url/api/documents?limit=100" -o "$work_dir/list.json"
python3 - "$work_dir/list.json" "$document_id" <<'PY'
import json
from pathlib import Path
import sys

page = json.loads(Path(sys.argv[1]).read_text())
assert any(document["id"] == sys.argv[2] for document in page["documents"])
PY

printf 'Compose upload/download checksum and container restart persistence: PASS\n'
