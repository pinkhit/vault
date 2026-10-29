#!/bin/sh
# Only a read-only snapshot of numeric disk statistics crosses into Docker.
set -eu
cd "$(dirname "$0")/.."
project_dir=$(pwd)
binary="$project_dir/.runtime/bin/host-storage"
installed_binary="$HOME/Library/Application Support/FamilyVault/bin/host-storage"
metrics_dir="$HOME/Library/Application Support/FamilyVault/host-storage"
label=com.familyvault.host-storage
domain="gui/$(id -u)"
agent_file="$HOME/Library/LaunchAgents/$label.plist"
action=${1:-start}

if test "$(uname -s)" != Darwin; then echo 'Run this script on the Mac, outside Docker.' >&2; exit 1; fi
case "$action" in
    status)
        if launchctl print "$domain/$label" >/dev/null 2>&1; then echo 'macOS storage collector is scheduled'; else echo 'macOS storage collector is stopped'; fi
        exit 0 ;;
    start|once|build|stop) ;;
    *) echo 'Usage: scripts/macos-storage.sh [start|stop|status|once|build]' >&2; exit 1 ;;
esac
if test -f "$agent_file"; then
    registered=$(/usr/libexec/PlistBuddy -c 'Print :ProgramArguments:0' "$agent_file")
    if test "$registered" != "$installed_binary"; then echo 'A different collector is registered; refusing to replace it.' >&2; exit 1; fi
fi
if test "$action" = stop; then
    launchctl bootout "$domain/$label" 2>/dev/null || true
    if test -f "$agent_file"; then rm "$agent_file"; fi
    exit 0
fi
case "$(uname -m)" in arm64) host_arch=arm64 ;; x86_64) host_arch=amd64 ;; *) exit 1 ;; esac
mkdir -p .runtime/bin .runtime/host-storage
chmod 755 .runtime/host-storage
docker build --target host-storage-export --build-arg "HOST_ARCH=$host_arch" --output type=local,dest=.runtime/bin .
if test "$action" = build; then exit 0; fi
set -- "$binary" -output-dir "$project_dir/.runtime/host-storage"
if test -n "${FAMILYVAULT_DOCKER_DISK:-}"; then set -- "$@" -docker-disk "$FAMILYVAULT_DOCKER_DISK"; fi
if test "$action" = once; then exec "$@"; fi
# launchd must not execute or write in a privacy-protected Documents folder.
launchctl bootout "$domain/$label" 2>/dev/null || true
mkdir -p "$(dirname "$installed_binary")" "$metrics_dir"
chmod 755 "$metrics_dir"
cp "$binary" "$installed_binary"
chmod 755 "$installed_binary"
if test -L .runtime/host-storage; then
    if test "$(readlink .runtime/host-storage)" != "$metrics_dir"; then echo 'Unexpected host-storage link; refusing to replace it.' >&2; exit 1; fi
else
    mv .runtime/host-storage .runtime/host-storage-local
    ln -s "$metrics_dir" .runtime/host-storage
fi
set -- "$installed_binary" -output-dir "$metrics_dir"
if test -n "${FAMILYVAULT_DOCKER_DISK:-}"; then set -- "$@" -docker-disk "$FAMILYVAULT_DOCKER_DISK"; fi
"$@" -print-launch-agent > .runtime/host-storage.plist
plutil -lint .runtime/host-storage.plist
mkdir -p "$HOME/Library/LaunchAgents"
cp .runtime/host-storage.plist "$agent_file"
launchctl bootstrap "$domain" "$agent_file"
echo 'macOS storage collector scheduled every 30 seconds and at login. Use this script with stop to remove it.'
