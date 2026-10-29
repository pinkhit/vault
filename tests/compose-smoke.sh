#!/bin/sh
# Uses a disposable Compose project; never bootstraps or deletes the developer's data.
set -eu
cd "$(dirname "$0")/.."
exec python3 tests/container_smoke.py
