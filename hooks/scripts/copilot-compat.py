#!/usr/bin/env python3
"""
copilot-compat.py — what GitHub Copilot CLI needs to run this plugin's Claude
Code-format commands, skills and agents. Every mode is a no-op under Claude
Code: it acts only when COPILOT_PLUGIN_ROOT is set, which Copilot exports to
plugin hooks and Claude Code does not.

WHY. Claude Code substitutes `${CLAUDE_PLUGIN_ROOT}` in the text of a command,
skill or agent before the model reads it. Copilot substitutes it only in hook
and MCP configuration; in a skill or command body the model sees the literal
placeholder, and the shell expands it to an empty string (`/scripts/x.py`).
Copilot does export the variable to hooks, and honours a Claude-format
PreToolUse `updatedInput`, so the substitution is made here, on the command,
just before it runs.

MODES

  pretool   PreToolUse on Bash. Replaces `${CLAUDE_PLUGIN_ROOT}` /
            `$CLAUDE_PLUGIN_ROOT` with this plugin's root when every occurrence
            names a file that exists under it (an occurrence that does not is
            another plugin's, and the command is left alone).
            Copilot asks the user before running a rewritten command unless the
            hook also says "allow". It says so only for the shape Claude Code's
            `allowed-tools` lines pre-approve: one `python3 <root>/scripts/<x>.py
            …` invocation with no shell operators, substitutions or redirections.
            A call that writes outside this machine is still asked for:
            `pr_post.py` (GitHub), `fairmind_connect.py` (the bind) and
            `fairmind_cli.py` with `--yes` or `--send` (a Fairmind write).
            Everything else gets the rewrite and Copilot's own permission flow.
            A command that already spells the root out (the session context
            gives it to the model) gets the same approval, without a rewrite.

  session   SessionStart. Emits `additionalContext` (Copilot reads the top-level
            field) with the plugin root and how this plugin's Claude Code tool
            names map to Copilot's, so the model can follow the commands as
            written.

Never blocks: any error is a silent exit 0, which leaves the call to Copilot.
"""

from __future__ import annotations

import json
import os
import re
import shlex
import sys

_PLACEHOLDER = re.compile(r'\$\{CLAUDE_PLUGIN_ROOT\}|\$CLAUDE_PLUGIN_ROOT(?![A-Za-z0-9_])')
#: What follows a placeholder: an optional closing quote, then a relative path.
_TAIL = re.compile(r'"?/([A-Za-z0-9_./-]+)')

#: Scripts whose effect leaves this machine: never auto-approved.
_ALWAYS_ASK = {"pr_post.py", "fairmind_connect.py"}
_FAIRMIND_WRITE_FLAGS = {"--yes", "--send"}

#: Claude Code agent file -> the display name its commands and skills use.
_AGENTS = {
    "tech-lead": "Technical Lead / Architect",
    "software-engineer": "Software Engineer",
    "code-reviewer": "Code Reviewer",
    "qa-engineer": "QA Engineer",
    "debug-detective": "Debugging Specialist",
    "cybersec-engineer": "Security Engineer",
}


def _root():
    if not os.environ.get("COPILOT_PLUGIN_ROOT"):
        return None  # not Copilot: Claude Code already substituted the text
    root = os.environ.get("CLAUDE_PLUGIN_ROOT") or os.environ.get("COPILOT_PLUGIN_ROOT")
    return os.path.realpath(root) if root and os.path.isdir(root) else None


def rewrite(command, root):
    """The command with every placeholder replaced, or None when it has none or
    one of them does not name a file of this plugin."""
    matches = list(_PLACEHOLDER.finditer(command))
    if not matches:
        return None
    for match in matches:
        tail = _TAIL.match(command, match.end())
        if not tail:
            return None
        target = os.path.normpath(os.path.join(root, tail.group(1)))
        if not target.startswith(root + os.sep) or not os.path.exists(target):
            return None
    return _PLACEHOLDER.sub(lambda _m: root, command)


def _plain(command):
    """True when the command has no operator, substitution, redirection or
    expansion outside single quotes: one simple command."""
    single = double = False
    for ch in command:
        if single:
            single = ch != "'"
            continue
        if ch == "'" and not double:
            single = True
        elif ch == '"':
            double = not double
        elif ch in "$`":
            return False
        elif not double and ch in ";&|<>()\n\r":
            return False
    return not single and not double


def auto_allow(command, root):
    if not _plain(command):
        return False
    try:
        argv = shlex.split(command)
    except ValueError:
        return False
    if len(argv) < 2 or argv[0] != "python3":
        return False
    script = os.path.realpath(argv[1])
    scripts_dir = os.path.join(root, "scripts")
    if os.path.dirname(script) != scripts_dir or not script.endswith(".py") \
            or not os.path.isfile(script):
        return False
    name = os.path.basename(script)
    if name in _ALWAYS_ASK:
        return False
    if name == "fairmind_cli.py" and _FAIRMIND_WRITE_FLAGS & set(argv[2:]):
        return False
    return True


def pretool(payload, root):
    if payload.get("tool_name") != "Bash":
        return None
    tool_input = payload.get("tool_input")
    if not isinstance(tool_input, dict) or not isinstance(tool_input.get("command"), str):
        return None
    command = rewrite(tool_input["command"], root)
    out = {"hookEventName": "PreToolUse"}
    if command is None:
        # No placeholder. The model may have written the root out itself (the
        # session context gives it); the same shape is pre-approved either way.
        command = tool_input["command"]
        if not auto_allow(command, root):
            return None
    else:
        updated = dict(tool_input)
        updated["command"] = command
        out["updatedInput"] = updated
    if auto_allow(command, root):
        out["permissionDecision"] = "allow"
        out["permissionDecisionReason"] = "fairmind-coding plugin script (pre-approved like its allowed-tools)"
    return {"hookSpecificOutput": out}


def session(root):
    agents = "; ".join(f"`{name}` -> agent_type `fairmind-coding:{stem}`"
                       for stem, name in _AGENTS.items())
    text = f"""fairmind-coding plugin, running in GitHub Copilot CLI. Its commands, skills and agents were written for Claude Code; read them with this mapping:
- Plugin root: {root}. Wherever an instruction says the plugin root or ${{CLAUDE_PLUGIN_ROOT}} is filled in for you (for example a gate descriptor that needs a literal absolute script path), use this path. Shell commands may keep the ${{CLAUDE_PLUGIN_ROOT}} placeholder exactly as written: a hook substitutes it before they run.
- `Task` (dispatch a sub-agent) is your Agent/task tool. The plugin agents are: {agents}. Each dispatch is one level deep from you, as the commands say.
- `AskUserQuestion` is asking the user (your ask_user tool, or a plain question when none is available). `TodoWrite` is your plan/todo tracking. `allowed-tools` frontmatter is not enforced here; follow each command's rules about what it may do anyway.
- The command banners Claude Code prints from a UserPromptExpansion hook do not exist in Copilot. Do not print them yourself; start with the command's first real step.
- Reach Fairmind through the `fairmind` CLI as the `fairmind-cli` skill describes; there is usually no Fairmind MCP mount here."""
    return {"additionalContext": text}


def main():
    mode = sys.argv[1] if len(sys.argv) > 1 else ""
    root = _root()
    if not root:
        return 0
    try:
        payload = json.load(sys.stdin) if not sys.stdin.isatty() else {}
    except ValueError:
        payload = {}
    if mode == "pretool":
        out = pretool(payload if isinstance(payload, dict) else {}, root)
    elif mode == "session":
        out = session(root)
    else:
        return 0
    if out:
        sys.stdout.write(json.dumps(out))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception:
        sys.exit(0)
