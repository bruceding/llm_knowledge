# Newsletter IMAP Import — Design Spec

## Overview

Add newsletter import via IMAP to the llm-knowledge system. Users configure their email IMAP credentials, specify a mailbox folder (default "Newsletter"), and the system periodically fetches unread emails, parses HTML content into markdown, and stores them as Documents.

V1 scope: IMAP + password/app-specific password authentication. OAuth2 deferred to V2.

## Data Model

### New table: `IMAPConfig`

One record per user.

```go
type IMAPConfig struct {
    ID            uint      `gorm:"primaryKey"`
    UserID        uint      `gorm:"uniqueIndex;not null"`
    Host          string    // e.g. imap.gmail.com
    Port          int       // e.g. 993
    Username      string    // e.g. user@gmail.com
    EncryptedPass string    // AES-256-GCM encrypted password
    FolderName    string    `gorm:"default:Newsletter"`
    AutoSync      bool      `gorm:"default:false"`
    LastSyncAt    time.Time
    CreatedAt     time.Time
    UpdatedAt     time.Time
}
```

### Document table (no schema change)

Newsletter emails stored as Documents with:

| Field | Value |
|-------|-------|
| `SourceType` | `"newsletter"` |
| `SourceURL` | "View in browser" link extracted from email HTML (empty if not found) |
| `SourceGUID` | Email Message-ID header (dedup key) |
| `RSSFeedID` | 0 (not used) |
| `Metadata` | JSON: `{"from": "...", "subject": "...", "date": "...", "messageId": "..."}` |
| `Status` | `"inbox"` |

## Password Encryption

New package: `backend/crypto/`

- AES-256-GCM symmetric encryption
- Key source (in priority order):
  1. `ENCRYPT_KEY` environment variable (hex-encoded 32 bytes)
  2. Auto-generated key file at `~/.llm-knowledge/encrypt.key` (created on first use)
- Two exported functions: `Encrypt(plaintext string) (string, error)` and `Decrypt(ciphertext string) (string, error)`
- Ciphertext stored as base64
- Implementation: Go stdlib `crypto/aes` + `crypto/cipher` + `crypto/rand`

## IMAP Sync Flow

### Connection

1. Dial IMAP server with TLS (`imapclient.DialTLS`)
2. Login with username + decrypted password
3. Select the configured folder (e.g. "Newsletter")

### First sync (`LastSyncAt` is zero)

1. `SEARCH UNSEEN` — get all unread message UIDs
2. Sort by date descending, take only the **10 most recent**
3. Process each message (see below)
4. Mark all 10 as `\Seen`
5. Set `LastSyncAt = now`
6. All older unread emails are ignored permanently

### Subsequent syncs

1. `SEARCH UNSEEN SINCE {LastSyncAt}` — only new unread emails
2. Process all matching messages
3. Mark processed messages as `\Seen`
4. Update `LastSyncAt = now`

### Per-message processing

1. Extract envelope: From, Subject, Date, Message-ID
2. Dedup check: query `Document` where `source_guid = Message-ID AND user_id = ?` (including soft-deleted, same as RSS)
3. Extract HTML body part (prefer `text/html`, fallback to `text/plain`)
4. Extract "view in browser" link from HTML:
   - Search for `<a>` tags with text matching patterns: "view in browser", "view online", "在浏览器中查看", "view this email in your browser"
   - Store as `SourceURL` (enables "o" keyboard shortcut in DocDetail)
5. Process HTML content:
   - Filter decorative images: skip images with width or height < 50px, or tracking pixels (1x1, single-pixel)
   - Download content images to `raw/newsletter/{sanitized-sender}/assets/`
   - Convert HTML to markdown using existing `processHTMLToMarkdown`
6. Save markdown file to `raw/newsletter/{sanitized-sender}/{sanitized-subject}.md`
7. Create Document record
8. Auto-create tags from sender name (e.g. tag "BestBlogs.dev")
9. Async summary generation (reuse existing `ingest.GenerateSummary`)

### IMAP Library

`github.com/emersion/go-imap/v2` + `github.com/emersion/go-imap/v2/imapclient`

## API Endpoints

All under `/api` group with auth middleware.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/imap/config` | Get current user's IMAP config (password field masked) |
| `PUT` | `/imap/config` | Create or update IMAP config |
| `DELETE` | `/imap/config` | Delete IMAP config and associated inbox-status documents (published/archived docs preserved, same as RSS feed deletion) |
| `POST` | `/imap/test` | Test IMAP connection (verify host/port/credentials/folder) |
| `POST` | `/imap/sync` | Manually trigger sync, return sync result |

### PUT `/imap/config` request body

```json
{
  "host": "imap.gmail.com",
  "port": 993,
  "username": "user@gmail.com",
  "password": "app-specific-password",
  "folderName": "Newsletter",
  "autoSync": true
}
```

Password is encrypted before storage. If password field is empty on update, existing password is preserved.

### POST `/imap/test` response

```json
{
  "success": true,
  "folderExists": true,
  "unseenCount": 42,
  "message": "Connection successful, folder 'Newsletter' has 42 unread messages"
}
```

Or on failure:

```json
{
  "success": false,
  "message": "Login failed: invalid credentials"
}
```

### POST `/imap/sync` response

Same structure as RSS `SyncResult`:

```json
{
  "newArticles": 5,
  "total": 10,
  "downloadErrors": 1,
  "message": "Synced 5 new newsletters"
}
```

## Auto Sync

Follows RSS pattern: `NewsletterHandler.StartAutoSyncScheduler()` runs a goroutine with a 1-hour ticker. On each tick, query all `IMAPConfig` records with `AutoSync=true`, skip if synced within the last hour, sync the rest.

Registered in `main.go` alongside the RSS scheduler.

## Frontend

### Settings Page — new "Newsletter (IMAP)" section

Added to `SettingsPage.tsx` as a new card below existing sections:

- **IMAP Server** — text input (placeholder: `imap.gmail.com`)
- **Port** — number input (default: `993`)
- **Username** — text input (placeholder: `user@gmail.com`)
- **Password** — password input (placeholder: `App password or authorization code`)
- **Folder Name** — text input (default: `Newsletter`)
- **Auto Sync** — toggle switch
- **Test Connection** button — calls `/api/imap/test`, shows success/failure inline
- **Save** button — calls `PUT /api/imap/config`

Helper text below password field: "Gmail: use app-specific password. QQ/163: use authorization code."

### Import Page — new "Newsletter" block

Added to `ImportView.tsx` as a new section:

- If IMAP not configured: show message "Configure IMAP in Settings to import newsletters" with link to settings
- If configured: show connection status, last sync time, "Sync Now" button
- Sync result displayed in the existing success toast pattern

### API client additions (`api.ts`)

```typescript
// Newsletter IMAP API
export async function getIMAPConfig(): Promise<IMAPConfig>
export async function updateIMAPConfig(config: IMAPConfigInput): Promise<IMAPConfig>
export async function deleteIMAPConfig(): Promise<void>
export async function testIMAPConnection(): Promise<IMAPTestResult>
export async function syncNewsletter(): Promise<SyncResult>
```

## File Structure (new files)

```
backend/
  crypto/
    crypto.go          # AES-256-GCM encrypt/decrypt
    crypto_test.go
  api/
    newsletter.go      # NewsletterHandler: CRUD + sync + test + scheduler
```

## Error Handling

- IMAP connection failures: log error, return to caller, don't crash scheduler
- Individual email parse failures: skip email, log error, continue with next
- Image download failures: increment error counter, continue (same as RSS)
- Encryption key missing: auto-generate on first use, log warning

## Testing

- `crypto/crypto_test.go`: encrypt/decrypt round-trip, wrong key detection
- Manual test: configure Gmail with app password, verify sync works
