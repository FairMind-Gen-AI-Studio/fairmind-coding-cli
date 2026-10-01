#!/usr/bin/env python3
"""
fairmind_cli.py — ONE definition of "how this plugin reaches Fairmind through
the `fairmind` CLI", stdlib only.

WHY IT EXISTS. Every Fairmind call this plugin makes is made by the
orchestrator (a command body or a skill), never by a sub-agent. Those calls
used to have exactly one transport: the `mcp__<fairmind-mount>__<Tool>` tools of
a per-project MCP entry. The `fairmind` CLI (bundled under `cli/`) speaks to the
SAME MCP server from the shell, holds its token in the OS credential store, and
exposes every tool by its exact MCP name (`fairmind tools call <Tool>`). So a
developer who installed the CLI and ran `fairmind auth login` can use the
commands and skills without configuring an MCP server in Claude Code.

RESOLUTION ORDER (first hit wins):

  1. `$FAIRMIND_CLI` — an executable, or a `.js` entry run under `node`.
  2. `fairmind` on PATH — the installed CLI (`./install-cli.sh`).
  3. The bundled Node edition, `<plugin>/cli/node/bin/fairmind.js`, when a
     `node` >= 20 is on PATH. Nothing to install: the plugin cache carries it.
  4. The bundled Go edition, `<plugin>/cli/bin/fairmind`, when it was built.

USAGE

    fairmind_cli.py --probe [--online]
        One JSON line: {available, edition, path, ...}. With --online it also
        runs `fairmind auth status --json` and reports whether a token is present,
        unexpired and accepted by the server. Exit 0 = usable, 1 = no CLI,
        2 = CLI present but not authenticated / server refused, 7 = server
        unreachable.

    fairmind_cli.py --send <Tool> --from <payload.json> --category <cat> [--batch N]
        Send one category of an `insights_flush_payload.py --emit ... --out`
        file through `fairmind tools call <Tool> --args-file - --yes --json`.
        The payload goes over stdin, so its size never meets argv or the Bash
        output cap. Exit 0 only when the CLI exited 0, the envelope says
        `ok: true` and the answer is not a bare `{"message": ...}` refusal
        (the MCP doors report refusals that way, with no status code).

    fairmind_cli.py --save <file> <fairmind arguments...>
        Run the CLI (with --json added) and, on success, write the envelope's
        `data` (exactly what the MCP tool returns) to <file>, printing only a
        one-line summary. On failure the envelope is printed and nothing is
        written. This is how a large read lands in a run's inbox without
        passing through the ~30 KB Bash output cap.

    fairmind_cli.py <any fairmind arguments...>
        Passthrough: exec the resolved CLI with these arguments, e.g.
        `fairmind_cli.py tools call Studio_get_task --args-json '{"task_id":"…"}' --json`.
        With no CLI, prints a CLI_NOT_FOUND envelope and exits 127.

WHAT IT NEVER DOES: read, print or pass a token. Authentication is the CLI's
business (`fairmind auth login`); this file only locates and invokes it.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys

_HERE = os.path.dirname(os.path.abspath(__file__))
PLUGIN_ROOT = os.path.dirname(_HERE)
BUNDLED_NODE_ENTRY = os.path.join(PLUGIN_ROOT, "cli", "node", "bin", "fairmind.js")
BUNDLED_GO_BINARY = os.path.join(PLUGIN_ROOT, "cli", "bin", "fairmind")

#: The CLI's own minimum (`engines.node` in cli/node/package.json).
_MIN_NODE_MAJOR = 20

#: Sent as `X-FairMind-Agent` so the platform can tell this plugin's calls from
#: a Copilot session's. Only a default — a value the user exported wins.
_AGENT_NAME = "claude-code/fairmind-coding"

#: CLI exit codes this script relays (docs/CLI.md "Exit codes").
EXIT_AUTH = 2
EXIT_UNAVAILABLE = 7
EXIT_NO_CLI = 127
#: Ours: the CLI succeeded but the tool answered with a refusal message.
EXIT_REFUSED = 9

_PROBE_TIMEOUT_S = 45
_SEND_TIMEOUT_S = 120


def _node_major(node):
    try:
        out = subprocess.run([node, "--version"], capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.SubprocessError):
        return None
    text = (out.stdout or "").strip().lstrip("v")
    try:
        return int(text.split(".", 1)[0])
    except ValueError:
        return None


def _node_prefix(entry):
    node = shutil.which("node")
    if not node:
        return None
    major = _node_major(node)
    if major is None or major < _MIN_NODE_MAJOR:
        return None
    return [node, entry]


def resolve():
    """`{"argv": [...], "edition": ..., "path": ...}` for the CLI to run, or
    None when none is usable. Never raises."""
    explicit = os.environ.get("FAIRMIND_CLI", "").strip()
    if explicit:
        path = os.path.expanduser(explicit)
        if path.endswith(".js") and os.path.isfile(path):
            prefix = _node_prefix(path)
            if prefix:
                return {"argv": prefix, "edition": "node", "path": path, "source": "FAIRMIND_CLI"}
        elif os.path.isfile(path) and os.access(path, os.X_OK):
            return {"argv": [path], "edition": "unknown", "path": path, "source": "FAIRMIND_CLI"}
        found = shutil.which(explicit)
        if found:
            return {"argv": [found], "edition": "unknown", "path": found, "source": "FAIRMIND_CLI"}

    on_path = shutil.which("fairmind")
    if on_path:
        return {"argv": [on_path], "edition": "installed", "path": on_path, "source": "PATH"}

    if os.path.isfile(BUNDLED_NODE_ENTRY):
        prefix = _node_prefix(BUNDLED_NODE_ENTRY)
        if prefix:
            return {"argv": prefix, "edition": "node", "path": BUNDLED_NODE_ENTRY, "source": "bundled"}

    if os.path.isfile(BUNDLED_GO_BINARY) and os.access(BUNDLED_GO_BINARY, os.X_OK):
        return {"argv": [BUNDLED_GO_BINARY], "edition": "go", "path": BUNDLED_GO_BINARY, "source": "bundled"}
    return None


def is_available():
    """Cheap local check: a CLI is resolvable. No network, no token read."""
    return resolve() is not None


def _env():
    env = dict(os.environ)
    env.setdefault("FAIRMIND_AGENT", _AGENT_NAME)
    return env


def _envelope_error(code, message, **extra):
    error = {"code": code, "message": message, "retryable": False}
    error.update(extra)
    return {"ok": False, "data": None, "error": error}


def _not_found_envelope():
    return _envelope_error(
        "CLI_NOT_FOUND",
        "the fairmind CLI is not available: install it with ./install-cli.sh from the "
        "fairmind-coding-cli repository (or put Node >= 20 on PATH to use the bundled "
        "copy), then run `fairmind auth login`",
    )


def run(args, stdin_text=None, timeout=_SEND_TIMEOUT_S):
    """Run the CLI with `args` (`--json` is the caller's job). Returns
    `(exit_code, envelope_or_None, stderr_text)`. Never raises."""
    found = resolve()
    if not found:
        return EXIT_NO_CLI, _not_found_envelope(), ""
    try:
        proc = subprocess.run(found["argv"] + list(args), input=stdin_text, capture_output=True,
                              text=True, timeout=timeout, env=_env())
    except subprocess.TimeoutExpired:
        return 8, _envelope_error("TIMEOUT", f"the fairmind CLI did not answer within {timeout}s",
                                  retryable=True), ""
    except OSError as exc:
        return EXIT_NO_CLI, _envelope_error("CLI_NOT_RUNNABLE", str(exc)), ""
    envelope = None
    text = (proc.stdout or "").strip()
    if text:
        try:
            envelope = json.loads(text)
        except ValueError:
            envelope = None
    return proc.returncode, envelope, proc.stderr or ""


def is_refusal(data):
    """A bare `{"message": ...}` answer: how the MCP doors report a refusal."""
    return isinstance(data, dict) and set(data) == {"message"}


def probe(online):
    found = resolve()
    if not found:
        print(json.dumps({"available": False, "hint": _not_found_envelope()["error"]["message"]}))
        return 1
    result = {"available": True, "edition": found["edition"], "source": found["source"],
              "path": found["path"]}
    if not online:
        print(json.dumps(result))
        return 0
    # `auth status`, not `status`: only the former asks the server to accept
    # the token (it lists the projects the token can see).
    code, envelope, _stderr = run(["auth", "status", "--json"], timeout=_PROBE_TIMEOUT_S)
    data = envelope.get("data") if isinstance(envelope, dict) else None
    token = data.get("token") if isinstance(data, dict) else None
    server = data.get("server") if isinstance(data, dict) else None
    token = token if isinstance(token, dict) else {}
    server = server if isinstance(server, dict) else {}
    claims = token.get("claims") if isinstance(token.get("claims"), dict) else {}
    result.update({
        "token_present": bool(token.get("present")),
        "token_source": token.get("source"),
        "token_expired": bool(claims.get("expired")),
        "token_expires_at": claims.get("expires_at"),
        "scope": claims.get("scope"),
        "server_ok": bool(server.get("ok")),
    })
    if code != 0 and isinstance(envelope, dict) and isinstance(envelope.get("error"), dict):
        result["error"] = {k: envelope["error"].get(k) for k in ("code", "message", "retryable")}
    print(json.dumps(result))
    if not result["token_present"] or result["token_expired"]:
        return EXIT_AUTH
    if code in (7, 8):
        return EXIT_UNAVAILABLE
    if code != 0 or not result["server_ok"]:
        return EXIT_AUTH if code in (0, 2, 4) else code
    return 0


def _pick_payload(path, category, batch):
    with open(path, encoding="utf-8") as handle:
        out = json.load(handle)
    if not isinstance(out, dict) or category not in out:
        raise ValueError(f"{path} has no '{category}' category")
    value = out[category]
    if value is None:
        raise ValueError(f"category '{category}' is null: nothing to send")
    if isinstance(value, list):
        if batch is None:
            raise ValueError(f"category '{category}' is a list of {len(value)} batch(es); "
                             "pass --batch <index> and send each one separately")
        if not 0 <= batch < len(value):
            raise ValueError(f"--batch {batch} is out of range (0..{len(value) - 1})")
        value = value[batch]
    elif batch is not None:
        raise ValueError(f"category '{category}' is a single payload; drop --batch")
    if not isinstance(value, dict):
        raise ValueError(f"the '{category}' payload is not a JSON object")
    return value


def send(argv):
    import argparse
    parser = argparse.ArgumentParser(prog="fairmind_cli.py --send")
    parser.add_argument("tool")
    parser.add_argument("--from", dest="source", required=True)
    parser.add_argument("--category", required=True)
    parser.add_argument("--batch", type=int, default=None)
    args = parser.parse_args(argv)
    try:
        payload = _pick_payload(args.source, args.category, args.batch)
    except (OSError, ValueError) as exc:
        print(json.dumps(_envelope_error("PAYLOAD_INVALID", str(exc))))
        return 5
    code, envelope, stderr = run(["tools", "call", args.tool, "--args-file", "-", "--yes", "--json"],
                                 stdin_text=json.dumps(payload))
    if envelope is None:
        envelope = _envelope_error("INVALID_RESPONSE", (stderr or "no JSON on stdout").strip()[-500:])
        code = code or 1
    if code == 0 and (not envelope.get("ok") or is_refusal(envelope.get("data"))):
        message = envelope.get("data", {}).get("message") if is_refusal(envelope.get("data")) else None
        envelope = _envelope_error("TOOL_REFUSED", message or "the tool did not report success")
        code = EXIT_REFUSED
    print(json.dumps(envelope))
    return code


def save(argv):
    if len(argv) < 2:
        print(json.dumps(_envelope_error("USAGE", "usage: --save <file> <fairmind arguments...>")))
        return 5
    path, args = argv[0], argv[1:]
    if "--json" not in args:
        args = args + ["--json"]
    code, envelope, stderr = run(args)
    if envelope is None:
        envelope = _envelope_error("INVALID_RESPONSE", (stderr or "no JSON on stdout").strip()[-500:])
        code = code or 1
    if code != 0 or not envelope.get("ok") or is_refusal(envelope.get("data")):
        if code == 0:
            data = envelope.get("data")
            envelope = _envelope_error("TOOL_REFUSED", data["message"] if is_refusal(data)
                                       else "the tool did not report success")
            code = EXIT_REFUSED
        print(json.dumps(envelope))
        return code
    text = json.dumps(envelope.get("data"), indent=2, ensure_ascii=False) + "\n"
    directory = os.path.dirname(os.path.abspath(path))
    os.makedirs(directory, exist_ok=True)
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(text)
    summary = {"ok": True, "saved": path, "bytes": len(text.encode("utf-8"))}
    if envelope.get("page"):
        summary["page"] = envelope["page"]
    print(json.dumps(summary))
    return 0


def main(argv=None):
    argv = list(sys.argv[1:] if argv is None else argv)
    if argv[:1] == ["--probe"]:
        return probe(online="--online" in argv[1:])
    if argv[:1] == ["--which"]:
        found = resolve()
        if not found:
            return 1
        print(found["path"])
        return 0
    if argv[:1] == ["--send"]:
        return send(argv[1:])
    if argv[:1] == ["--save"]:
        return save(argv[1:])
    found = resolve()
    if not found:
        print(json.dumps(_not_found_envelope()))
        return EXIT_NO_CLI
    sys.stdout.flush()
    os.execve(found["argv"][0], found["argv"] + argv, _env())
    return 1  # unreachable


if __name__ == "__main__":
    sys.exit(main())
