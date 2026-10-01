#!/usr/bin/env bash
# copilot-block.sh <hook> [args...] — run a blocking Stop/SubagentStop hook so it
# blocks under GitHub Copilot CLI too.
#
# Claude Code blocks a Stop or SubagentStop on exit 2 and feeds stderr back to the
# model. Copilot CLI treats exit 2 on those events as a WARNING and lets the turn
# end; it blocks only on `{"decision":"block","reason":...}` on stdout. So under
# Copilot (COPILOT_PLUGIN_ROOT is set for plugin hooks, never by Claude Code) an
# exit 2 is turned into that JSON, with the hook's stderr as the reason. Under
# Claude Code the hook is exec'd untouched: same stdin, stdout, stderr and exit.
if [ -z "${COPILOT_PLUGIN_ROOT:-}" ]; then
  exec "$@"
fi

ERR=$(mktemp "${TMPDIR:-/tmp}/fm-hook-err.XXXXXX") || exec "$@"
"$@" 2>"$ERR"
RC=$?
if [ "$RC" -eq 2 ]; then
  python3 -c 'import json, sys
reason = open(sys.argv[1], encoding="utf-8", errors="replace").read().strip()
print(json.dumps({"decision": "block", "reason": reason or "blocked by the fairmind-coding hook"}))' "$ERR"
  PRC=$?
  if [ "$PRC" -ne 0 ]; then
    # No python3 to encode the reason: still block, with a fixed one.
    printf '{"decision":"block","reason":"blocked by the fairmind-coding hook (see its stderr)"}\n'
  fi
  rm -f "$ERR"
  exit 0
fi
cat "$ERR" >&2
rm -f "$ERR"
exit "$RC"
