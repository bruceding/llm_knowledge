# Claude CLI Security Hook - Deployment Guide

## Overview

This security hook restricts Claude CLI's file-access tools (Read, Glob, Grep, LS) to only access files within a user's allowed directory, preventing unauthorized access to system files (e.g., `/etc/shadow`, `~/.ssh/`) in multi-user environments.

**Fail-closed design:** If `ALLOWED_DIR` is not set, ALL file access is denied. If the hook script is missing, security hooks are disabled with a warning log.

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                    Linux Server Deployment                       │
├─────────────────────────────────────────────────────────────────┤
│  /opt/llm-knowledge/                                            │
│  ├── backend                    # Go binary                      │
│  ├── scripts/                                                   │
│  │   └── path-validator.py     # Security hook script           │
│  └── data/                                                      │
│      └── users/{userId}/        # Per-user isolated directories  │
│          ├── raw/                                               │
│          ├── wiki/                                              │
│          └── ...                                                │
└─────────────────────────────────────────────────────────────────┘
```

## How It Works

### 1. Hook Loading Flow (Settings file generated once at startup)

```
Go Backend Startup
    │
    │ InitSecurityConfig(scriptsDir)
    │   → Generate /tmp/claude-security-{random}.json (ONE TIME)
    │   → Cache in globalSecurityConfig.SettingsPath
    │
    ↓
User Request (Doc Chat / Query)
    │
    │ GetSettingsPath() → Returns cached path
    │ BuildSecureEnv(userDir) → Returns full env with ALLOWED_DIR=/data/users/{userId}
    │
    │ exec.CommandContext(..., --settings {cachedPath}, ...)
    │
    ↓
Claude CLI Process (each user gets own process with own ALLOWED_DIR)
    │
    │ Load hooks from cached settings.json
    │ Read ALLOWED_DIR from environment
    │
    │ File tool called → PreToolUse hook validates path
    │   → No ALLOWED_DIR? → DENY (fail-closed)
    │   → Sensitive path? → DENY
    │   → Outside ALLOWED_DIR? → DENY
    │   → Inside ALLOWED_DIR? → ALLOW
```

### 2. Path Validation Logic

```python
# path-validator.py checks (fail-closed):
# 1. ALLOWED_DIR must be set — otherwise deny all access
# 2. Sensitive path patterns (defense-in-depth, always blocked)
#    - /etc/shadow, /etc/passwd, ~/.ssh/, ~/.aws/, etc.
#    - /private/etc/* on macOS (symlink target of /etc)
# 3. Allowed directory restriction (with path boundary check)
#    - Path must be within ALLOWED_DIR (exact match or ALLOWED_DIR + os.sep prefix)
#    - Handles relative paths, symlinks, path traversal (../)
```

### 3. Hooked Tools

| Tool | Input Fields Validated |
|------|----------------------|
| Read | `file_path` |
| Glob | `path` (root dir), `pattern` (fallback) |
| Grep | `path` (root dir) |
| LS   | `path` (directory) |

## Deployment Steps

### 1. Copy Hook Script to Server

```bash
# On the server
mkdir -p /opt/llm-knowledge/scripts

# Copy from repository
cp backend/scripts/path-validator.py /opt/llm-knowledge/scripts/
chmod +x /opt/llm-knowledge/scripts/path-validator.py
```

### 2. Initialize Security Config at Startup

In `main.go`:

```go
import "llm-knowledge/claude"

func main() {
    cfg := config.Load()

    // Initialize security hooks
    // The script path can be configured via environment variable
    scriptsDir := os.Getenv("LLM_SCRIPTS_DIR")
    if scriptsDir == "" {
        scriptsDir = "/opt/llm-knowledge/scripts"
    }
    if err := claude.InitSecurityConfig(scriptsDir); err != nil {
        log.Fatalf("Security initialization failed: %v", err)
    }

    // ... rest of initialization
}
```

### 3. Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `LLM_SCRIPTS_DIR` | Directory containing hook scripts | `/opt/llm-knowledge/scripts` |
| `ALLOWED_DIR` | Per-user allowed directory (set by Go code, **required**) | User's data directory |

### 4. Docker/Container Deployment

```dockerfile
# In Dockerfile
COPY backend/scripts/path-validator.py /opt/llm-knowledge/scripts/
RUN chmod +x /opt/llm-knowledge/scripts/path-validator.py

# Environment variable
ENV LLM_SCRIPTS_DIR=/opt/llm-knowledge/scripts
```

## Security Patterns Blocked

The hook always blocks access to these paths (defense-in-depth, even within ALLOWED_DIR):

### System Paths (Linux)
- `/etc/shadow`, `/etc/passwd`, `/etc/gshadow`
- `/etc/ssh/`, `/etc/ssl/private`
- `/root/`
- `/proc/`, `/sys/`, `/var/log/`

### System Paths (macOS — symlink targets)
- `/private/etc/shadow`, `/private/etc/passwd`, `/private/etc/gshadow`
- `/private/etc/ssh/`, `/private/etc/ssl/private`
- `/private/var/log/`

### User Credentials
- `~/.ssh/` (SSH keys)
- `~/.aws/` (AWS credentials)
- `~/.config/gcloud/` (GCP credentials)
- `~/.kube/` (Kubernetes configs)
- `~/.docker/` (Docker configs)
- `~/.gnupg/` (GPG keys)
- `~/.netrc`, `~/.pgpass`

## Verification

### Test on Server

```bash
# 1. Verify script exists
ls -la /opt/llm-knowledge/scripts/path-validator.py

# 2. Test hook manually — blocked path (should exit 2)
ALLOWED_DIR="/opt/llm-knowledge/data/users/1" \
python3 /opt/llm-knowledge/scripts/path-validator.py <<< '{"tool_name": "Read", "tool_input": {"file_path": "/etc/shadow"}}'
# Expected: {"decision": "deny", "reason": "Access denied: sensitive file"} (exit code 2)

# 3. Test hook manually — allowed path (should exit 0)
ALLOWED_DIR="/opt/llm-knowledge/data/users/1" \
python3 /opt/llm-knowledge/scripts/path-validator.py <<< '{"tool_name": "Read", "tool_input": {"file_path": "/opt/llm-knowledge/data/users/1/wiki/test.md"}}'
# Expected: exit code 0 (no output)

# 4. Test hook manually — missing ALLOWED_DIR (should exit 2)
python3 /opt/llm-knowledge/scripts/path-validator.py <<< '{"tool_name": "Read", "tool_input": {"file_path": "/tmp/test"}}'
# Expected: {"decision": "deny", "reason": "ALLOWED_DIR not configured — access denied by default"} (exit code 2)
```

## Troubleshooting

### Hook not working

1. Check script path: `claude.InitSecurityConfig()` logs the scripts directory
2. Check Python availability: `python3 --version` (required on server)
3. Check environment: `ALLOWED_DIR` must be set in the Claude CLI process environment
4. Check `--settings` flag: must be passed to Claude CLI (added by `GetSettingsPath()`)

### Permission denied errors for valid files

1. Verify `ALLOWED_DIR` matches user's actual directory
2. Check symlink resolution (script uses `os.path.realpath`)
3. Check relative path handling (resolved relative to `ALLOWED_DIR`)

## ⚠️ Known Security Gaps

### `--bare` mode (Client.Send)

The `Client.Send` method uses `--bare` which skips all PreToolUse hooks. This is used for automated ingest scenarios that need Write/Edit access. Compensating controls:
- `--allowedTools` restricts tool availability
- `cmd.Dir` restricts the working directory
- Prompt content is system-constructed

### Missing scripts directory

If the scripts directory is not found at startup, `InitSecurityConfig` logs a warning but continues without hooks. **In production deployments, consider making this a fatal error** to prevent the service from running without path restrictions.

## Cleanup

The settings file is automatically cleaned up on server shutdown via `claude.CleanupSecuritySettings()`.
