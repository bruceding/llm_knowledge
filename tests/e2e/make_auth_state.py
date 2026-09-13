#!/usr/bin/env python3
"""
Generate Playwright storage-state files for the e2e suite from existing DB sessions.

Why this exists
---------------
tests/e2e/conftest.py refreshes `.auth/state.json` by opening a browser and
waiting for a human to type credentials **and a captcha**. That works, but it
means any test needing a *specific* role (admin vs. normal user) cannot be run
unattended, and the shared state file only ever holds whoever logged in last.

Some tests need both roles at once — e.g. the admin-only LLM backend switch must
be visible to an admin and invisible to a normal user. So this script writes two
extra state files next to `state.json`:

    .auth/state-admin.json      newest valid session of a role='admin' user
    .auth/state-nonadmin.json   newest valid session of a role='user'  user

It also keeps conftest's own shared file alive:

    .auth/state.json            what `saved_auth_state` loads for every
                                `authenticated_page` / `mobile_page` fixture

When that one's token expires, conftest deletes it and opens a browser waiting
for a human to type credentials **and a captcha**, so the whole suite becomes
unrunnable unattended — even for tests that only assert on DOM. Reusing a session
the app already issued removes that step. An existing `state.json` whose token is
still valid is left alone, so running this never silently switches which account
the suite acts as.

It never creates, mutates or deletes anything: it opens the database read-only and
reuses sessions the app already issued (7-day TTL). No password is needed or read.

Usage
-----
    python3 tests/e2e/make_auth_state.py            # all three files
    python3 tests/e2e/make_auth_state.py --db PATH  # non-default database

If no valid session exists for a role, log in as that user in the browser once
(that creates the session) and re-run. Files land in tests/e2e/.auth/, which is
gitignored.
"""

import argparse
import datetime
import json
import pathlib
import sqlite3
import sys

ORIGIN = "http://localhost:9090"
AUTH_DIR = pathlib.Path(__file__).parent / ".auth"
DEFAULT_DB = pathlib.Path.home() / ".llm-knowledge" / "data" / "knowledge.db"

# role -> output file
TARGETS = {"admin": "state-admin.json", "user": "state-nonadmin.json"}

# conftest.py's AUTH_STATE_FILE: the shared state every authenticated fixture loads.
SHARED_STATE = "state.json"


def newest_valid_session(con: sqlite3.Connection, role: str):
    """Newest non-expired session belonging to a user with this role."""
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    return con.execute(
        """SELECT s.token, s.expires_at, u.id, u.username, u.must_change_password
             FROM sessions s JOIN users u ON u.id = s.user_id
            WHERE u.role = ? AND s.expires_at > ?
         ORDER BY s.id DESC LIMIT 1""",
        (role, now),
    ).fetchone()


def session_valid(con: sqlite3.Connection, token: str) -> bool:
    """True when this token is still a non-expired session.

    `expires_at` is compared as text, exactly like `newest_valid_session` does:
    the app always writes the same fixed-width format, so the date prefix decides.
    """
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    return con.execute(
        "SELECT 1 FROM sessions WHERE token = ? AND expires_at > ? LIMIT 1",
        (token, now),
    ).fetchone() is not None


def existing_token(path: pathlib.Path) -> str | None:
    """The `token` a storage-state file carries, or None when unreadable/absent."""
    try:
        state = json.loads(path.read_text())
    except (OSError, ValueError):
        return None
    for origin in state.get("origins", []):
        for item in origin.get("localStorage", []):
            if item.get("name") == "token":
                return item.get("value")
    return None


def write_state(filename: str, row, role: str, note: str = "") -> None:
    """Write one storage-state file from a `newest_valid_session` row."""
    token, expires, uid, username, must_change = row
    path = AUTH_DIR / filename
    path.write_text(json.dumps(
        storage_state(token, uid, username, bool(must_change)), indent=2))
    flag = "  ⚠ must_change_password=1 → the UI will force /change-password" \
        if must_change else ""
    print(f"  {filename:<22} OK    {username} (id={uid}, role={role}) "
          f"expires {expires[:19]}{flag}{note}")


def storage_state(token: str, user_id: int, username: str, must_change: bool) -> dict:
    """Mirror what the app's own login writes into localStorage.

    `auth-storage` is the zustand-persisted auth store; PrivateRoute reads
    `mustChangePassword` from it and redirects everything except /change-password
    when it is true. So it has to carry the real value, not a hardcoded false.
    """
    return {
        "cookies": [],
        "origins": [
            {
                "origin": ORIGIN,
                "localStorage": [
                    {"name": "token", "value": token},
                    {
                        "name": "auth-storage",
                        "value": json.dumps(
                            {
                                "state": {
                                    "isLoggedIn": True,
                                    "userId": user_id,
                                    "username": username,
                                    "mustChangePassword": must_change,
                                    "token": token,
                                },
                                "version": 0,
                            }
                        ),
                    },
                ],
            }
        ],
    }


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[1])
    ap.add_argument("--db", default=str(DEFAULT_DB), help="path to knowledge.db")
    args = ap.parse_args()

    if not pathlib.Path(args.db).exists():
        print(f"database not found: {args.db}", file=sys.stderr)
        return 2

    # mode=ro: this script must never be able to modify the real database.
    con = sqlite3.connect(f"file:{args.db}?mode=ro", uri=True)
    AUTH_DIR.mkdir(parents=True, exist_ok=True)

    missing = []
    rows = {}
    for role, filename in TARGETS.items():
        row = newest_valid_session(con, role)
        if not row:
            missing.append(role)
            print(f"  {filename:<22} SKIP  no valid session for role={role!r} "
                  f"(log in as such a user once, then re-run)")
            continue
        rows[role] = row
        write_state(filename, row, role)

    # conftest's shared file: same source as state-nonadmin.json, but only written
    # when the one on disk is gone or dead.
    token = existing_token(AUTH_DIR / SHARED_STATE)
    if token and session_valid(con, token):
        print(f"  {SHARED_STATE:<22} KEEP  existing token still valid")
    elif rows.get("user"):
        write_state(SHARED_STATE, rows["user"], "user",
                    "  (was missing or expired)")
    else:
        print(f"  {SHARED_STATE:<22} SKIP  no valid session for role='user'")

    con.close()
    return 1 if missing else 0


if __name__ == "__main__":
    sys.exit(main())
