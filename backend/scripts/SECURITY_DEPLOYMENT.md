# Claude CLI Security Hook - Deployment Guide

## Overview

This security hook restricts Claude CLI's `Read` tool to only access files within a user's allowed directory, preventing unauthorized access to system files (e.g., `/etc/shadow`, `~/.ssh/id_rsa`) in multi-user environments.

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

### 1. Hook Loading Flow (Optimized - Settings file generated once at startup)

```
Go Backend Startup
    │
    │ InitSecurityConfig(scriptsDir)
    │   → Generate /tmp/claude-security-{pid}.json (ONE TIME)
    │   → Cache in globalSecurityConfig.SettingsPath
    │
    ↓
User Request (Doc Chat / Query)
    │
    │ GetSettingsPath() → Returns cached path
    │ BuildSecureEnv(userDir) → Returns ALLOWED_DIR=/data/users/{userId}
    │
    │ exec.CommandContext(..., --settings {cachedPath}, ...)
    │
    ↓
Claude CLI Process (each user gets own process with own ALLOWED_DIR)
    │
    │ Load hooks from cached settings.json
    │ Read ALLOWED_DIR from environment
    │
    │ Read tool called → PreToolUse hook validates path
    │                     → Checks: sensitive path? outside ALLOWED_DIR? → DENY/ALLOW
```

### 2. Path Validation Logic

```python
# path-validator.py checks:
# 1. Sensitive path patterns (always blocked)
#    - /etc/shadow, /etc/passwd, ~/.ssh/, ~/.aws/, etc.
# 2. Allowed directory restriction
#    - Path must start with ALLOWED_DIR
#    - Handles relative paths, symlinks, path traversal (../)
```

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
    claude.InitSecurityConfig(scriptsDir)
    
    // ... rest of initialization
}
```

### 3. Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `LLM_SCRIPTS_DIR` | Directory containing hook scripts | `/opt/llm-knowledge/scripts` |
| `ALLOWED_DIR` | Per-user allowed directory (set by Go code) | User's data directory |

### 4. Docker/Container Deployment

```dockerfile
# In Dockerfile
COPY backend/scripts/path-validator.py /opt/llm-knowledge/scripts/
RUN chmod +x /opt/llm-knowledge/scripts/path-validator.py

# Environment variable
ENV LLM_SCRIPTS_DIR=/opt/llm-knowledge/scripts
```

## Security Patterns Blocked

The hook blocks access to:

### System Paths
- `/etc/shadow`, `/etc/passwd`, `/etc/gshadow`
- `/etc/ssh/`, `/etc/ssl/private`
- `/root/`
- `/proc/`, `/sys/`, `/var/log/`

### User Credentials
- `~/.ssh/` (SSH keys)
- `~/.aws/` (AWS credentials)
- `~/.config/gcloud/` (GCP credentials)
- `~/.kube/` (Kubernetes configs)
- `~/.docker/` (Docker configs)
- `~/.gnupg/` (GPG keys)
- `~/.netrc`, `~/.pgpass`

### File Patterns
- `.env` files
- `*_key.pem`, `*_key.rsa`, `*_key.pub`
- `id_rsa`, `id_dsa`, `id_ecdsa`, `id_ed25519`
- `credentials`, `credentials.json`, `secrets.json`
- `secret.key`, `.htpasswd`, `.pgpass`, `.my.cnf`

## Verification

### Test on Server

```bash
# 1. Verify script exists
ls -la /opt/llm-knowledge/scripts/path-validator.py

# 2. Test hook manually
ALLOWED_DIR="/opt/llm-knowledge/data/users/1" \
python3 /opt/llm-knowledge/scripts/path-validator.py <<< '{"tool_input": {"file_path": "/etc/shadow"}}'
# Expected output: {"decision": "deny", "reason": "..."} (exit code 2)

# 3. Test with Claude CLI (should be blocked)
cd /opt/llm-knowledge/data/users/1
ALLOWED_DIR="/opt/llm-knowledge/data/users/1" \
claude --settings /tmp/claude-security-test.json --allowedTools Read -p "读取 /etc/shadow"
```

## Troubleshooting

### Hook not working

1. Check script path: `claude.InitSecurityConfig()` logs the scripts directory
2. Check Python availability: `python3 --version` (required on server)
3. Check environment: `ALLOWED_DIR` must be set

### Permission denied errors for valid files

1. Verify `ALLOWED_DIR` matches user's actual directory
2. Check symlink resolution (script uses `os.path.realpath`)
3. Check relative path handling (resolved relative to `ALLOWED_DIR`)

## Fallback Behavior

If `scriptsDir` is not found or `path-validator.py` doesn't exist:
- Security hooks are disabled (no restriction)
- Log warning: `[security] Warning: scripts directory not found`
- Claude CLI runs without path restrictions

This ensures the service doesn't crash if security hooks are not deployed, but logs indicate the security gap.