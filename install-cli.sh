#!/usr/bin/env bash
# Install the `fairmind` CLI bundled in cli/ so the fairmind-coding plugin (and
# you, from a terminal) can reach Fairmind without an MCP server in Claude Code.
#
#   ./install-cli.sh            Node edition (recommended): npm install -g of a packed tarball
#   ./install-cli.sh --go       Go edition: build cli/bin/fairmind and copy it to $PREFIX (default ~/.local/bin)
#
# Then authenticate once: `pbpaste | fairmind auth login` (the token goes to the OS
# credential store) and check with `fairmind auth status`.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI="$ROOT/cli"

install_node() {
  command -v node >/dev/null || { echo "node not found: install Node.js >= 20 first" >&2; exit 1; }
  major="$(node -p 'process.versions.node.split(".")[0]')"
  if [ "$major" -lt 20 ]; then echo "Node.js >= 20 required (found $(node --version))" >&2; exit 1; fi
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  # `npm pack` runs the prepack hook (copy-templates) and produces a self-contained
  # tarball, so the global install does not link back into this checkout.
  (cd "$CLI/node" && npm pack --silent --pack-destination "$tmp" >/dev/null)
  npm install -g "$tmp"/*.tgz
}

install_go() {
  command -v go >/dev/null || { echo "go not found: install Go first" >&2; exit 1; }
  prefix="${PREFIX:-$HOME/.local/bin}"
  make -C "$CLI" build
  mkdir -p "$prefix"
  install -m 0755 "$CLI/bin/fairmind" "$prefix/fairmind"
  case ":$PATH:" in *":$prefix:"*) ;; *) echo "note: $prefix is not on PATH" >&2 ;; esac
}

case "${1:-}" in
  ""|--node) install_node ;;
  --go) install_go ;;
  -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
  *) echo "unknown option: $1 (use --node or --go)" >&2; exit 2 ;;
esac

echo
fairmind version --json 2>/dev/null || true
echo "Next: pbpaste | fairmind auth login   (then: fairmind auth status)"
