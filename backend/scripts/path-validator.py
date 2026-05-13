#!/usr/bin/env python3
"""
Path Validator Hook for Claude CLI

This PreToolUse hook validates file paths before file-access tools execute.
It restricts access to:
1. Only files within the ALLOWED_DIR (user's data directory)
2. Blocks access to sensitive system paths

Supports: Read, Glob, Grep, LS tools

Usage:
  Environment variables:
    ALLOWED_DIR - The allowed base directory for file access
                  (e.g., /opt/llm-knowledge/data/users/1)

  Deployed at: /opt/llm-knowledge/scripts/path-validator.py

  Loaded via settings.json:
    {
      "hooks": {
        "PreToolUse": [
          {"matcher": "Read",  "hooks": [{"type": "command", "command": "..."}]},
          {"matcher": "Glob",  "hooks": [{"type": "command", "command": "..."}]},
          {"matcher": "Grep",  "hooks": [{"type": "command", "command": "..."}]},
          {"matcher": "LS",    "hooks": [{"type": "command", "command": "..."}]}
        ]
      }
    }
"""

import json
import sys
import os
import re

# Sensitive path patterns - only applied to paths OUTSIDE ALLOWED_DIR
# Paths within ALLOWED_DIR are always allowed (user's own data)
SENSITIVE_PATH_PATTERNS = [
    # System configuration and secrets
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


def is_sensitive_path(path):
    """Check if path matches any sensitive pattern. Only for paths outside ALLOWED_DIR."""
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
    - Read: file_path
    - Glob: pattern (treated as a path)
    - Grep: path (root directory to search)
    - LS: path (directory to list)
    """
    paths = []
    if tool_name == "Read":
        fp = tool_input.get("file_path", "")
        if fp:
            paths.append(fp)
    elif tool_name == "Glob":
        pattern = tool_input.get("pattern", "")
        if pattern and not pattern.startswith("**"):  # Skip wildcard-only patterns
            paths.append(pattern)
    elif tool_name == "Grep":
        # Grep has 'path' (root dir) field
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

    # Resolve allowed_dir once
    if allowed_dir:
        try:
            allowed_dir = os.path.realpath(allowed_dir)
        except Exception:
            allowed_dir = os.path.normpath(allowed_dir)

    resolved_path = resolve_path(file_path, allowed_dir)

    # First check: allowed directory restriction (with proper path boundary)
    if allowed_dir and not is_path_within_dir(resolved_path, allowed_dir):
        # Path is outside allowed dir — check if it's also a sensitive path
        if is_sensitive_path(resolved_path):
            return False, "Access denied: sensitive file"
        return False, "Access denied: path outside allowed directory"

    # Path is within allowed dir — allow (user's own data)
    # Still check sensitive paths for defense-in-depth, but only block system paths
    if is_sensitive_path(resolved_path):
        return False, "Access denied: sensitive file"

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

    # Extract tool name and input
    tool_name = data.get("tool_name", "")
    tool_input = data.get("tool_input", {})

    if not isinstance(tool_input, dict):
        tool_input = {}

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
