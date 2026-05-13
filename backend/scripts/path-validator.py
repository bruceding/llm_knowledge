#!/usr/bin/env python3
"""
Path Validator Hook for Claude CLI

This PreToolUse hook validates file paths before the Read tool executes.
It restricts access to:
1. Only files within the ALLOWED_DIR (user's data directory)
2. Blocks access to sensitive system paths

Usage:
  Environment variables:
    ALLOWED_DIR - The allowed base directory for file access
                  (e.g., /opt/llm-knowledge/data/users/1)

  Deployed at: /opt/llm-knowledge/scripts/path-validator.py

  Loaded via settings.json:
    {
      "hooks": {
        "PreToolUse": [{
          "matcher": "Read",
          "hooks": [{
            "type": "command",
            "command": "/opt/llm-knowledge/scripts/path-validator.py"
          }]
        }]
      }
    }
"""

import json
import sys
import os
import re

# Linux sensitive path patterns - always block regardless of ALLOWED_DIR
SENSITIVE_PATTERNS = [
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

    # User credentials and secrets
    r'^/home/.*/\.ssh/',
    r'^/home/.*/\.aws/',
    r'^/home/.*/\.config/gcloud/',
    r'^/home/.*/\.kube/',
    r'^/home/.*/\.docker/',
    r'^/home/.*/\.gnupg/',
    r'^/home/.*/\.netrc$',
    r'^/home/.*/\.pgpass$',

    # Common secret file patterns (works on both Linux and macOS)
    r'\.env$',
    r'\.env\.',
    r'_key\.pem$',
    r'_key\.rsa$',
    r'_key\.pub$',
    r'private_key',
    r'private\.key',
    r'id_rsa$',
    r'id_dsa$',
    r'id_ecdsa$',
    r'id_ed25519$',
    r'credentials$',
    r'credentials\.json$',
    r'secrets\.json$',
    r'secret\.key$',
    r'\.htpasswd$',
    r'\.pgpass$',
    r'\.my.cnf$',
]

def is_sensitive_path(path):
    """Check if path matches any sensitive pattern."""
    # Normalize path for comparison
    norm_path = os.path.normpath(path)

    for pattern in SENSITIVE_PATTERNS:
        try:
            if re.search(pattern, norm_path, re.IGNORECASE):
                return True
        except:
            continue
    return False

def resolve_path(path, allowed_dir):
    """Resolve path to absolute real path."""
    # Handle relative paths
    if not path.startswith('/'):
        # Resolve relative to allowed_dir (which is cwd for Claude)
        path = os.path.join(allowed_dir, path)

    # Normalize and resolve symlinks
    try:
        path = os.path.realpath(path)
    except:
        path = os.path.normpath(path)

    return path

def validate_path(file_path, allowed_dir):
    """
    Validate file path against security rules.

    Returns (is_allowed, reason)
    """
    # Empty path - allow (no file to read)
    if not file_path:
        return True, None

    # Resolve allowed_dir to real path
    if allowed_dir:
        try:
            allowed_dir = os.path.realpath(allowed_dir)
        except:
            allowed_dir = os.path.normpath(allowed_dir)

    # Resolve the requested path
    resolved_path = resolve_path(file_path, allowed_dir)

    # First check: sensitive path patterns (always block)
    if is_sensitive_path(resolved_path):
        return False, f"Sensitive file access denied: {resolved_path} matches security policy"

    # Second check: allowed directory restriction
    if allowed_dir and not resolved_path.startswith(allowed_dir):
        return False, f"Access denied: '{resolved_path}' is outside allowed directory '{allowed_dir}'"

    return True, None

def main():
    # Read stdin input from Claude CLI
    try:
        input_data = sys.stdin.read()
        if not input_data:
            sys.exit(0)

        data = json.loads(input_data)
    except json.JSONDecodeError:
        # Invalid JSON - allow by default (Claude will handle)
        sys.exit(0)
    except:
        sys.exit(0)

    # Extract file_path from tool_input
    file_path = data.get("tool_input", {}).get("file_path", "")

    # Get allowed directory from environment
    allowed_dir = os.environ.get("ALLOWED_DIR", "")

    # Validate the path
    is_allowed, reason = validate_path(file_path, allowed_dir)

    if not is_allowed:
        # Output denial to stderr (Claude hook protocol)
        response = {
            "decision": "deny",
            "reason": reason
        }
        print(json.dumps(response), file=sys.stderr)
        sys.exit(2)  # Exit code 2 signals denial

    # Exit 0 = allow the operation
    sys.exit(0)

if __name__ == "__main__":
    main()