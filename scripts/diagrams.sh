#!/usr/bin/env bash
# Render the D2 diagrams of docs/design/diagrams/src to SVG, or check that the committed SVGs are current.
#
#   scripts/diagrams.sh render   write docs/design/diagrams/<name>.svg for every src/<name>.d2
#   scripts/diagrams.sh check    render into a scratch directory and fail on any difference, a source with no
#                                SVG, or an SVG with no source
#
# Every diagram is rendered with the ELK layout and one light plus one dark theme in a single SVG (it follows the
# viewer's colour scheme). The output depends on the d2 version, so the version is pinned below: bump it here,
# re-render, and commit the SVGs together. src/_style.d2 is imported by the diagrams and is not rendered itself.
set -euo pipefail

D2_VERSION="v0.9.0"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$ROOT/docs/design/diagrams/src"
OUT="$ROOT/docs/design/diagrams"

if ! command -v d2 >/dev/null 2>&1; then
  echo "diagrams: d2 $D2_VERSION is required (brew install d2, or https://d2lang.com/tour/install)" >&2
  exit 2
fi
have="$(d2 --version)"
if [ "$have" != "$D2_VERSION" ]; then
  echo "diagrams: d2 $D2_VERSION is required, found $have (the SVG output changes between versions)" >&2
  exit 2
fi

# render_all writes every diagram into the directory $1.
render_all() {
  local dest="$1" f name log
  for f in "$SRC"/[!_]*.d2; do
    name="$(basename "$f" .d2)"
    if ! log="$(d2 --layout=elk --theme=0 --dark-theme=200 --pad=24 "$f" "$dest/$name.svg" 2>&1)"; then
      echo "$log" >&2
      exit 1
    fi
  done
}

case "${1:-}" in
  render)
    render_all "$OUT"
    ;;
  check)
    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT
    render_all "$tmp"
    status=0
    for f in "$tmp"/*.svg; do
      name="$(basename "$f")"
      if ! cmp -s "$f" "$OUT/$name"; then
        echo "diagrams: $name is missing or stale; run make diagrams" >&2
        status=1
      fi
    done
    for f in "$OUT"/*.svg; do
      if [ ! -e "$tmp/$(basename "$f")" ]; then
        echo "diagrams: $(basename "$f") has no source in docs/design/diagrams/src" >&2
        status=1
      fi
    done
    exit "$status"
    ;;
  *)
    echo "usage: $0 render|check" >&2
    exit 2
    ;;
esac
