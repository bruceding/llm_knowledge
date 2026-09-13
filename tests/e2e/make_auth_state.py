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

It never creates, mutates or deletes anything: it opens the database read-only and
reuses sessions the app already issued (7-day TTL). No password is needed or read.

Usage
-----
    python3 tests/e2e/make_auth_state.py            # both files
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
    for role, filename in TARGETS.items():
        row = newest_valid_session(con, role)
        if not row:
            missing.append(role)
            print(f"  {filename:<22} SKIP  no valid session for role={role!r} "
                  f"(log in as such a user once, then re-run)")
            continue
        token, expires, uid, username, must_change = row
        path = AUTH_DIR / filename
        path.write_text(json.dumps(
            storage_state(token, uid, username, bool(must_change)), indent=2))
        flag = "  ⚠ must_change_password=1 → the UI will force /change-password" \
            if must_change else ""
        print(f"  {filename:<22} OK    {username} (id={uid}, role={role}) "
              f"expires {expires[:19]}{flag}")

    con.close()
    return 1 if missing else 0


if __name__ == "__main__":
    sys.exit(main())
