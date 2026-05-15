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
import ipaddress
import socket
from urllib.parse import urlparse

# Cap DNS resolution latency for WebFetch validation. The Claude CLI hook
# itself is bounded to 5s (HookMatcher.Timeout in security.go); a 2s cap on
# DNS leaves headroom for everything else and forecloses a slow-DNS attack:
# attacker-controlled NS responding slowly could otherwise push the hook past
# its outer timeout, and CLI fail-open behavior on hook timeout would become a
# bypass. setdefaulttimeout is process-global, but path-validator runs as a
# short-lived subprocess so global state has no spillover.
socket.setdefaulttimeout(2.0)

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
    r'^/home/.*/\.bash_history$',
    r'^/home/.*/\.zsh_history$',
    r'^/home/.*/\.python_history$',
    r'^/home/.*/\.config/git/credentials$',
    r'^/home/.*/\.git-credentials$',

    # User credentials - macOS
    r'^/Users/.*/\.ssh/',
    r'^/Users/.*/\.aws/',
    r'^/Users/.*/\.config/gcloud/',
    r'^/Users/.*/\.kube/',
    r'^/Users/.*/\.docker/',
    r'^/Users/.*/\.gnupg/',
    r'^/Users/.*/\.netrc$',
    r'^/Users/.*/\.pgpass$',
    r'^/Users/.*/\.bash_history$',
    r'^/Users/.*/\.zsh_history$',
    r'^/Users/.*/\.python_history$',
    r'^/Users/.*/\.config/git/credentials$',
    r'^/Users/.*/\.git-credentials$',
    r'^/Users/.*/Library/Keychains/',
]

# Pre-compile patterns for performance
_COMPILED_PATTERNS = [re.compile(p) for p in SENSITIVE_PATH_PATTERNS]

# Tools that are denied unconditionally (defense-in-depth backstop for --disallowedTools).
# These tools either bypass the directory sandbox (Bash → arbitrary shell commands) or
# accept inputs we cannot meaningfully validate from a hook (NotebookEdit). Production
# sessions must not use them; the CLI's --disallowedTools is the primary gate, this is
# a backstop in case the flag is misconfigured.
ALWAYS_DENIED_TOOLS = frozenset({"Bash", "BashOutput", "KillShell", "NotebookEdit", "SlashCommand", "Task"})

# WebFetch URL scheme allowlist. file://, gopher://, ftp:// etc. either let the
# tool read local files or talk to internal services, so we only allow plain HTTP(S).
ALLOWED_WEBFETCH_SCHEMES = frozenset({"http", "https"})


def is_blocked_ip(ip):
    """Return True if ip is a loopback/link-local/private address.

    Covers actual SSRF targets: loopback (127.0.0.0/8, ::1), link-local incl.
    AWS/GCP/Azure metadata at 169.254.169.254 (169.254.0.0/16, fe80::/10),
    RFC1918 private ranges, IPv6 ULA (fc00::/7), and 0.0.0.0 / :: which can
    route to localhost on some stacks.

    Intentionally NOT blocking is_reserved or is_multicast: those flags cover
    things like Teredo tunneling (2001::/32) which Google and other public
    services legitimately use, and would create false positives.
    """
    return (
        ip.is_loopback
        or ip.is_link_local
        or ip.is_private
        or ip.is_unspecified
    )


def validate_webfetch_url(url):
    """Validate a WebFetch URL for SSRF risk. Returns (is_allowed, reason).

    Defense-in-depth checks:
      1. Reject malformed/empty URLs.
      2. Restrict scheme to http/https — kills file:// and gopher:// pivots.
      3. Reject literal IP hosts that fall in private/internal ranges.
      4. For DNS hostnames, resolve all A/AAAA records and reject if ANY
         resolves to a private/internal IP. This is best-effort against
         DNS rebinding; the actual fetch will resolve again, so it can still
         be raced, but the static check rejects the obvious attack patterns.
    """
    if not url:
        return False, "Access denied: empty WebFetch URL"

    parsed = urlparse(url)
    scheme = (parsed.scheme or "").lower()
    if scheme not in ALLOWED_WEBFETCH_SCHEMES:
        return False, f"Access denied: WebFetch scheme '{parsed.scheme}' not allowed"

    host = parsed.hostname
    if not host:
        return False, "Access denied: WebFetch URL missing host"

    # Try as literal IP first — avoids unnecessary DNS lookups and catches
    # raw-IP SSRF like http://127.0.0.1:6379/.
    try:
        ip = ipaddress.ip_address(host)
        if is_blocked_ip(ip):
            return False, f"Access denied: WebFetch target {host} is private/internal"
        return True, None
    except ValueError:
        pass

    # DNS hostname: resolve and reject if any address is internal.
    try:
        addrinfo = socket.getaddrinfo(host, None)
    except Exception:
        return False, f"Access denied: WebFetch DNS resolution failed for {host}"

    for entry in addrinfo:
        ip_str = entry[4][0]
        try:
            ip = ipaddress.ip_address(ip_str)
        except ValueError:
            return False, f"Access denied: unparseable resolved IP {ip_str}"
        if is_blocked_ip(ip):
            return False, f"Access denied: host '{host}' resolves to private/internal IP {ip_str}"

    return True, None


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

    # WebFetch: validate URL for SSRF risk. WebFetch stays allowed (public
    # internet is a legitimate use case) but private/internal targets are blocked.
    if tool_name == "WebFetch":
        url = tool_input.get("url", "")
        is_allowed, reason = validate_webfetch_url(url)
        if not is_allowed:
            deny(reason)
        sys.exit(0)

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
