# baryon-mcp

An [MCP](https://modelcontextprotocol.io) server for Proton Mail, written in Go. It talks IMAP to the [Proton Mail Bridge](https://proton.me/mail/bridge) running on your machine, so Claude (or any MCP client) can browse and read your mail and save drafts.

*Why "baryon"? A proton is a baryon. All the obvious names were taken.*

## What it does

| Tool | Purpose |
|---|---|
| `list_folders` | List all folders in the mailbox |
| `list_emails` | List messages in a folder, newest first, with pagination |
| `search_emails` | Search by text, sender, recipient, subject, date range, or unread state |
| `get_email` | Read one message: metadata, decoded text/HTML bodies, attachment list |
| `list_attachments` | List a message's attachments without transferring content |
| `get_attachment` | Fetch one attachment (up to 1 MiB decoded; images returned natively) |
| `save_draft` | Create or replace a draft, with plain text, HTML, and attachments |

Draft saving is deliberately the **only write capability**. There are no tools to send, move, or generally delete messages or change flags. Read tools open mailboxes with IMAP `EXAMINE` and all fetches peek, so they cannot mark a message as read.

### Saving drafts

`save_draft` accepts a sender, optional To/Cc/Bcc recipients, subject, plain-text body, optional HTML alternative, and regular file attachments supplied as standard base64. The sender must be one of the Proton addresses available through the configured Bridge account.

Omit `uid` and `uidvalidity` to create a draft. To update one, pass both values from a Drafts `list_emails` or `search_emails` result and supply the complete desired draft again. Call `get_email` first to recover all recipients (including Bcc), bodies, and the attachment list, then use `get_attachment` for attachment content. Baryon preserves the previous Message-ID. It refuses to replace drafts carrying In-Reply-To or References headers because Bridge's IMAP draft-creation path cannot retain that reply-thread metadata. IMAP replacements receive a new UID: baryon appends the replacement first and only then deletes the previous UID, so a rejected upload cannot destroy the existing draft. If the replacement is saved but old-draft cleanup fails, the tool returns the new UID with `previous_draft_removed: false` and a warning about the possible duplicate.

The first version supports up to 10 regular attachments, 1 MiB decoded per attachment and 4 MiB decoded in total. Plain-text and HTML bodies are each capped at 50,000 characters. Inline CID images and attachment file paths are not supported.

## Security model

- **Loopback only.** The server refuses any Bridge host that isn't a loopback address; your credentials and mail never leave the machine through it.
- **TLS pinning, fail closed.** Bridge serves a self-signed certificate; baryon-mcp verifies the connection against a pinned copy of it. Without a pinned cert it refuses to start — an unverified connection would let any local process squatting Bridge's port capture the Bridge password. Insecure mode exists but only as an explicit opt-in.
- **Narrow write surface.** Draft discovery uses ordinary IMAP `LIST`, preferring the `\Drafts` special-use attribute when present and falling back to Proton Bridge's canonical `Drafts` mailbox name. Saving requires UIDPLUS, updates use UIDVALIDITY to reject stale identifiers, and cleanup uses targeted `UID EXPUNGE` rather than expunging a whole mailbox.
- **Bounded everything.** At most 4 concurrent IMAP connections; fetched text bodies capped at 768 KiB transfer / 50,000 characters; fetched attachments refused above 1.5 MiB encoded before transfer and above 1 MiB decoded after decoding; saved bodies and attachments use the limits above; canceled requests abort their IMAP session immediately, including while waiting for draft serialization.
- **Scoped credentials.** Bridge's generated IMAP password is used — never your Proton account password.

## Requirements

- [Proton Mail Bridge](https://proton.me/mail/bridge) installed, logged in, and running (requires a paid Proton Mail plan)
- For building from source: Go 1.26+

## Setup

### 1. Get your Bridge credentials

In Bridge, open your account's mailbox configuration. Note the IMAP **username** and the generated **password** (not your Proton account password).

### 2. Export Bridge's TLS certificate

In Bridge: **Settings → Advanced settings → Export TLS certificates**, and save `cert.pem` somewhere stable.

Alternatively, capture it from the running Bridge:

```sh
openssl s_client -starttls imap -connect 127.0.0.1:1143 -showcerts </dev/null 2>/dev/null \
  | openssl x509 -outform PEM > bridge-cert.pem
```

### 3a. Install the MCPB bundle (Claude Desktop)

Grab the bundle for your platform from releases and open it with Claude Desktop:

- `baryon-mcp-darwin.mcpb` — macOS (universal: Apple Silicon + Intel)
- `baryon-mcp-linux-amd64.mcpb` / `baryon-mcp-linux-arm64.mcpb` — pick your CPU architecture
- `baryon-mcp-windows-amd64.mcpb` — Windows (ARM Windows runs it via built-in emulation)

The install dialog asks for the username, password (stored in your OS keychain), and the exported certificate.

### 3b. Or configure a stdio MCP server manually

```json
{
  "mcpServers": {
    "baryon": {
      "command": "/path/to/baryon-mcp",
      "env": {
        "PROTON_BRIDGE_USERNAME": "you@proton.me",
        "PROTON_BRIDGE_PASSWORD": "bridge-generated-password",
        "PROTON_BRIDGE_TLS_CERT": "/path/to/cert.pem"
      }
    }
  }
}
```

## Configuration reference

| Env var | Default | Notes |
|---|---|---|
| `PROTON_BRIDGE_USERNAME` | — | required |
| `PROTON_BRIDGE_PASSWORD` | — | required; Bridge's generated password |
| `PROTON_BRIDGE_HOST` | `127.0.0.1` | loopback addresses only |
| `PROTON_BRIDGE_IMAP_PORT` | `1143` | |
| `PROTON_BRIDGE_IMAP_SECURITY` | `starttls` | `tls` if Bridge is in SSL connection mode |
| `PROTON_BRIDGE_TLS_CERT` | — | path to Bridge's exported cert; effectively required |
| `PROTON_BRIDGE_ALLOW_INSECURE` | `false` | explicit opt-out of certificate verification |

## Building from source

```sh
make build            # binary for this machine
make test             # gofmt + vet + race-enabled tests
make snapshot         # goreleaser dry-run: binaries, archives, .mcpb bundles into dist/
```

Releases are built by [GoReleaser](https://goreleaser.com) in CI: pushing a `v*` tag cross-compiles all platforms (macOS as a universal binary), packs the `.mcpb` bundles, and publishes everything to a GitHub release. `make snapshot` runs the same pipeline locally and needs `goreleaser`, `jq`, and `npx`.

## Design notes

- One fresh IMAP connection per tool call (mailbox selection is per-connection state), bounded by a semaphore.
- Read tools select with `EXAMINE`. Draft saving selects only the discovered Drafts mailbox read-write and serializes saves so concurrent replacements cannot race on one stale UID.
- Message content is never fetched via whole-message `BODY[]`: the extended `BODYSTRUCTURE` is walked first, and only the needed MIME parts cross the wire, with size limits enforced from the structure's encoded sizes *before* transfer.
- `list_emails`/`search_emails` return the folder's `uidvalidity`, and the per-message tools require it back — a Bridge cache rebuild changes it, and the mismatch error tells the client to re-list instead of silently reading the wrong message.

## License

MIT
