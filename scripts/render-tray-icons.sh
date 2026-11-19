#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
source_svg=app/internal/server/web/favicon.svg
target=app/cmd/quote0-link
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

sed '/id="tile"/d; s/#e6efd9/#000000/g' "$source_svg" > "$work/template.svg"
sips -s format png -z 32 32 "$source_svg" --out "$target/tray-color.png" >/dev/null
sips -s format png -z 32 32 "$work/template.svg" --out "$target/tray-template.png" >/dev/null
