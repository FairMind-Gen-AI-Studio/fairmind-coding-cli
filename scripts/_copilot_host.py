#!/usr/bin/env python3
"""
_copilot_host.py — which GitHub Copilot CLI session is which sub-agent. Stdlib
only, a leaf like `_binding`: hooks import it on their fast path.

WHY. Under Claude Code a sub-agent's tool calls carry `agent_type`, and only
the main session fires `Stop`. Under Copilot CLI a sub-agent runs as its OWN
session: its tool-use payloads carry that session's id and no agent name, and
its end fires a `Stop` of its own as well as the parent's `SubagentStop`.
Copilot records the link in the PARENT session's `events.jsonl`: a
`subagent.started` event whose `agentId` is the sub-agent's session id, with
`agentName` (plugin:file) and `agentDisplayName`.

`SubagentStart` (which Copilot fires after that event, with the parent's
`transcriptPath`) calls `record`, which files one small marker per sub-agent
session under the temp directory. The trace hook then names the agent behind a
tool call, and the loop gate leaves a sub-agent's own `Stop` alone (only the
session driving the loop may be gated by it, or come to own it).

Everything here is Copilot-only (COPILOT_PLUGIN_ROOT is exported to plugin hooks
by Copilot, never by Claude Code), best-effort and never raises.

CLI
    _copilot_host.py record            SubagentStart payload on stdin
    _copilot_host.py is-subagent <id>  exit 0 when <id> is a known sub-agent session
"""

from __future__ import annotations

import json
import os
import re
import sys
import tempfile
import time

_SAFE_ID = re.compile(r"^[A-Za-z0-9_.-]{1,128}$")


def is_copilot():
    return bool(os.environ.get("COPILOT_PLUGIN_ROOT"))


def agents_dir():
    return os.path.join(tempfile.gettempdir(), "fairmind-copilot-agents")


def _name(data):
    """`plugin:Display Name`, the spelling Claude Code gives the same agent."""
    agent = data.get("agentName") or data.get("agentType") or ""
    display = data.get("agentDisplayName") or ""
    if not display:
        return agent or None
    prefix = agent.split(":")[0] + ":" if ":" in agent else ""
    return prefix + display


#: How long a parent transcript stays worth scanning after its SubagentStart.
_PARENT_TTL_S = 24 * 3600


def _parents_dir():
    return os.path.join(agents_dir(), "parents")


def _scan(transcript_path, offset):
    """Markers for every `subagent.started` past `offset`; returns the offset
    to resume from (only complete lines are consumed)."""
    directory = agents_dir()
    with open(transcript_path, "rb") as handle:
        handle.seek(offset)
        chunk = handle.read()
    end = chunk.rfind(b"\n") + 1
    for raw in chunk[:end].splitlines():
        if b'"subagent.started"' not in raw:
            continue
        try:
            event = json.loads(raw.decode("utf-8", errors="replace"))
        except ValueError:
            continue
        agent_id = event.get("agentId")
        data = event.get("data") if isinstance(event.get("data"), dict) else {}
        name = _name(data)
        if not isinstance(agent_id, str) or not _SAFE_ID.match(agent_id) or not name:
            continue
        path = os.path.join(directory, agent_id)
        if not os.path.exists(path):
            with open(path, "w", encoding="utf-8") as out:
                out.write(name)
    return offset + end


def record(transcript_path):
    """Called from SubagentStart with the PARENT's transcript. Copilot fires the
    hook before that transcript's `subagent.started` line reaches the disk, so
    the parent is remembered here and scanned (incrementally) when a session
    nobody has named yet makes its first tool call."""
    if not transcript_path or not os.path.isfile(transcript_path):
        return
    try:
        os.makedirs(_parents_dir(), exist_ok=True)
        key = re.sub(r"[^A-Za-z0-9_.-]", "-", os.path.abspath(transcript_path))[-180:]
        state = os.path.join(_parents_dir(), key + ".json")
        if not os.path.exists(state):
            with open(state, "w", encoding="utf-8") as out:
                json.dump({"path": os.path.abspath(transcript_path), "offset": 0}, out)
        _refresh()
    except OSError:
        return


def _refresh():
    """Scan every recently registered parent transcript past its offset."""
    parents = _parents_dir()
    if not os.path.isdir(parents):
        return
    now = time.time()
    for entry in os.listdir(parents):
        state = os.path.join(parents, entry)
        try:
            if now - os.path.getmtime(state) > _PARENT_TTL_S:
                os.remove(state)
                continue
            with open(state, encoding="utf-8") as handle:
                info = json.load(handle)
            path, offset = info.get("path"), int(info.get("offset") or 0)
            if not isinstance(path, str) or not os.path.isfile(path) or os.path.getsize(path) <= offset:
                continue
            new_offset = _scan(path, offset)
            with open(state, "w", encoding="utf-8") as out:
                json.dump({"path": path, "offset": new_offset}, out)
        except (OSError, ValueError, TypeError):
            continue


def subagent_name(session_id):
    """The agent behind a Copilot sub-agent session, or None."""
    if not is_copilot() or not isinstance(session_id, str) or not _SAFE_ID.match(session_id):
        return None
    marker = os.path.join(agents_dir(), session_id)
    if not os.path.exists(marker):
        try:
            _refresh()
        except OSError:
            return None
    try:
        with open(marker, encoding="utf-8") as handle:
            return handle.read().strip() or None
    except OSError:
        return None


def main(argv):
    if argv[:1] == ["record"]:
        if not is_copilot():
            return 0  # a hook: under Claude Code, silently nothing to do
        try:
            payload = json.load(sys.stdin)
        except ValueError:
            return 0
        if isinstance(payload, dict):
            record(payload.get("transcriptPath") or payload.get("transcript_path"))
        return 0
    if argv[:1] == ["is-subagent"] and len(argv) > 1:
        return 0 if subagent_name(argv[1]) else 1
    return 1


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except Exception:
        sys.exit(1)
