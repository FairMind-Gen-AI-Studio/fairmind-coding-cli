# AGENTS.md

This repository's guidance for AI coding agents (Codex, and any agents.md-aware
tool) lives in [CLAUDE.md](CLAUDE.md) — read it before working on the code.

Quick reference:
- Two editions in parity: Node.js (`node/`, primary) and Go (root). Mirror any
  change across both; keep `go test -race ./...` and `cd node && npm test` green.
- Default mode calls the FairMind MCP server directly; no third-party deps;
  never leak the token; TLS always verified. See CLAUDE.md for the invariants.
