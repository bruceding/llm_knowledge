#!/usr/bin/env python3
"""
Path Validator Hook for Claude CLI

This PreToolUse hook validates file paths before file-access tools execute.
It restricts access to:
1. Only files within the ALLOWED_DIR (user's data directory)
2. Blocks access to sensitive system paths

Fail-closed: if ALLOWED_DIR is not set, ALL access is denied.

Supports:
  - Path-bound tools (Read, Write, Edit, Glob, Grep, LS): validated against ALLOWED_DIR
  - Always-denied tools (Bash, BashOutput, KillShell, NotebookEdit, SlashCommand, Task):
    denied unconditionally as defense-in-depth. These should also be on the CLI's
    --disallowedTools list; the hook is a backstop. The set MUST mirror
    DangerousDisallowedTools in backend/claude/security.go.

Usage:
  Environment variables:
    ALLOWED_DIR - The allowed base directory for file access
                  (e.g., /opt/llm-knowledge/data/users/1)
                  REQUIRED — hook denies all access if not set.

  Deployed at: /opt/llm-knowledge/scripts/path-validator.py
"""

import json
import sys
import os
import re

# Sensitive path patterns (defense-in-depth, checked even within ALLOWED_DIR)
# These cover both direct paths and macOS /private symlink targets
SENSITIVE_PATH_PATTERNS = [
    # System configuration and secrets (Linux)
    r'^/etc/shadow$',
    r'^/etc/passwd$',
    r'^/etc/gshadow$',
    r'^/etc/ssh/',
    r'^/etc/ssl/(private|certs)',
    r'^/root/',
    r'^/proc/',
    r'^/sys/',
    r'^/var/log/',
    r'^/var/run/',

    # macOS symlink targets (/etc -> /private/etc, /var -> /private/var)
    r'^/private/etc/shadow$',
    r'^/private/etc/passwd$',
    r'^/private/etc/gshadow$',
    r'^/private/etc/ssh/',
    r'^/private/etc/ssl/(private|certs)',
    r'^/private/var/log/',
    r'^/private/var/run/',

    # User credentials - Linux
    r'^/home/.*/\.ssh/',
    r'^/home/.*/\.aws/',
    r'^/home/.*/\.config/gcloud/',
    r'^/home/.*/\.kube/',
    r'^/home/.*/\.docker/',
    r'^/home/.*/\.gnupg/',
    r'^/home/.*/\.netrc$',
    r'^/home/.*/\.pgpass$',

    # User credentials - macOS
    r'^/Users/.*/\.ssh/',
    r'^/Users/.*/\.aws/',
    r'^/Users/.*/\.config/gcloud/',
    r'^/Users/.*/\.kube/',
    r'^/Users/.*/\.docker/',
    r'^/Users/.*/\.gnupg/',
    r'^/Users/.*/\.netrc$',
    r'^/Users/.*/\.pgpass$',
]

# Pre-compile patterns for performance
_COMPILED_PATTERNS = [re.compile(p) for p in SENSITIVE_PATH_PATTERNS]

# Tools that are denied unconditionally (defense-in-depth backstop for --disallowedTools).
# These tools either bypass the directory sandbox (Bash → arbitrary shell commands) or
# accept inputs we cannot meaningfully validate from a hook (NotebookEdit). Production
# sessions must not use them; the CLI's --disallowedTools is the primary gate, this is
# a backstop in case the flag is misconfigured.
ALWAYS_DENIED_TOOLS = frozenset({"Bash", "BashOutput", "KillShell", "NotebookEdit", "SlashCommand", "Task"})


def is_sensitive_path(path):
    """Check if path matches any sensitive pattern."""
    norm_path = os.path.normpath(path)
    for compiled in _COMPILED_PATTERNS:
        try:
            if compiled.search(norm_path):
                return True
        except Exception:
            continue
    return False


def is_path_within_dir(resolved_path, allowed_dir):
    """Check if resolved_path is within allowed_dir using proper path boundary.

    Uses os.sep to prevent prefix collision (e.g., /data/users/1 matching /data/users/10).
    """
    if not allowed_dir:
        return False
    return resolved_path == allowed_dir or resolved_path.startswith(allowed_dir + os.sep)


def extract_paths_from_input(tool_name, tool_input):
    """Extract file paths from tool_input based on tool type.

    Different tools use different field names:
    - Read/Write/Edit: file_path
    - Glob: path (root dir) + pattern (glob pattern, used as fallback path hint)
    - Grep: path (root directory to search)
    - LS: path (directory to list)
    """
    paths = []
    if tool_name in ("Read", "Write", "Edit"):
        fp = tool_input.get("file_path", "")
        if fp:
            paths.append(fp)
    elif tool_name == "Glob":
        # Glob tool accepts both 'path' (root dir) and 'pattern' (glob expression)
        # 'path' takes priority as it explicitly sets the search root
        p = tool_input.get("path", "")
        if p:
            paths.append(p)
        else:
            # Fallback: extract base path from pattern (before first wildcard)
            pattern = tool_input.get("pattern", "")
            if pattern and not pattern.startswith("**"):
                paths.append(pattern)
    elif tool_name == "Grep":
        p = tool_input.get("path", "")
        if p:
            paths.append(p)
    elif tool_name == "LS":
        p = tool_input.get("path", "")
        if p:
            paths.append(p)
    return paths


def resolve_path(path, allowed_dir):
    """Resolve path to absolute real path."""
    if not path.startswith('/'):
        path = os.path.join(allowed_dir, path) if allowed_dir else os.path.abspath(path)

    try:
        path = os.path.realpath(path)
    except Exception:
        path = os.path.normpath(path)

    return path


def validate_path(file_path, allowed_dir):
    """Validate file path against security rules. Returns (is_allowed, reason)."""
    if not file_path:
        return True, None

    # Fail-closed: no allowed_dir means no access
    if not allowed_dir:
        return False, "Access denied: no allowed directory configured"

    # Resolve allowed_dir once
    try:
        allowed_dir = os.path.realpath(allowed_dir)
    except Exception:
        allowed_dir = os.path.normpath(allowed_dir)

    resolved_path = resolve_path(file_path, allowed_dir)

    # Defense-in-depth: block sensitive paths regardless of location
    if is_sensitive_path(resolved_path):
        return False, "Access denied: sensitive file"

    # Directory boundary check (with proper path boundary via os.sep)
    if not is_path_within_dir(resolved_path, allowed_dir):
        return False, "Access denied: path outside allowed directory"

    return True, None


def deny(reason):
    """Output denial and exit with code 2."""
    response = {"decision": "deny", "reason": reason}
    print(json.dumps(response), file=sys.stderr)
    sys.exit(2)


def main():
    # Fail-closed: deny by default on any error
    try:
        input_data = sys.stdin.read()
    except Exception:
        deny("Failed to read input")

    if not input_data:
        # Empty input is valid — no tool call to validate
        sys.exit(0)

    try:
        data = json.loads(input_data)
    except json.JSONDecodeError:
        deny("Invalid JSON input")
    except Exception:
        deny("Parse error")

    if not isinstance(data, dict):
        deny("Invalid input format")

    # Get allowed directory from environment
    allowed_dir = os.environ.get("ALLOWED_DIR", "")

    # Fail-closed: deny if ALLOWED_DIR is not configured
    if not allowed_dir:
        deny("ALLOWED_DIR not configured — access denied by default")

    # Extract tool name and input
    tool_name = data.get("tool_name", "")
    tool_input = data.get("tool_input", {})

    if not isinstance(tool_input, dict):
        tool_input = {}

    # Backstop: deny tools that should never run in production sessions.
    # The CLI's --disallowedTools should already block these; this hook is defense-in-depth.
    if tool_name in ALWAYS_DENIED_TOOLS:
        deny(f"Access denied: tool '{tool_name}' is not permitted")

    # Extract paths based on tool type
    paths = extract_paths_from_input(tool_name, tool_input)

    # Validate each path
    for file_path in paths:
        is_allowed, reason = validate_path(file_path, allowed_dir)
        if not is_allowed:
            deny(reason)

    # All paths allowed
    sys.exit(0)


if __name__ == "__main__":
    main()
