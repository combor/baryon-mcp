# Baryon MCP

[![License](https://img.shields.io/github/license/combor/baryon-mcp)](LICENSE)
[![CI](https://github.com/combor/baryon-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/combor/baryon-mcp/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/combor/baryon-mcp)](https://github.com/combor/baryon-mcp/releases/latest)

An MCP server for reading Proton Mail and saving drafts through a local [Proton Mail Bridge](https://proton.me/mail/bridge).

Baryon runs over stdio and connects to Bridge over IMAP. Draft saving is its only mailbox write capability; there are no general send, move, delete, or flag-management tools.

## Tools

| Tool | Description |
|---|---|
| `list_folders` | List mailbox folders |
| `list_emails` | List messages in a folder, newest first, with pagination |
| `search_emails` | Search by text, sender, recipient, subject, date, or unread state |
| `get_email` | Read metadata, Bcc recipients, plain-text/HTML bodies, and attachment metadata |
| `list_attachments` | List attachment metadata without downloading content |
| `get_attachment` | Fetch one attachment, up to 25 MB decoded |
| `save_draft` | Create or replace a draft with text, HTML, Bcc recipients, and attachments from base64 or local file paths |

## Requirements

- Proton Mail Bridge installed, signed in, and running locally
- The IMAP username and generated password shown in Bridge's mailbox settings
- Bridge's exported TLS certificate for a verified connection

Building from source requires Go 1.26.5 or later.

## Installation

### Claude Desktop

Download the `.mcpb` bundle for your platform from the [latest release](https://github.com/combor/baryon-mcp/releases/latest), open it, and enter the Bridge settings when prompted.

### Claude Code and Codex

The installers download the latest platform archive, verify its SHA-256 checksum, install a credential-backed launcher, and configure installed Claude Code and Codex CLIs.

macOS or Linux:

```sh
(
  set -e
  installer=$(mktemp "${TMPDIR:-/tmp}/baryon-install.XXXXXX")
  trap 'rm -f "$installer"' EXIT
  curl -fsSL https://raw.githubusercontent.com/combor/baryon-mcp/main/scripts/install.sh -o "$installer"
  sh "$installer"
)
```

Windows PowerShell:

```powershell
$installer = Join-Path ([IO.Path]::GetTempPath()) ("baryon-install-{0}.ps1" -f [Guid]::NewGuid().ToString("N"))
try {
  Invoke-WebRequest -UseBasicParsing -Uri https://raw.githubusercontent.com/combor/baryon-mcp/main/scripts/install.ps1 -OutFile $installer
  powershell.exe -NoProfile -ExecutionPolicy Bypass -File $installer
} finally {
  Remove-Item -Force $installer
}
```

Use `--client claude` or `--client codex` on macOS/Linux, or `-Client claude` / `-Client codex` on Windows, to configure only one client. Existing `baryon` entries are preserved unless `--force-client-config` or `-ForceClientConfig` is supplied.

Credential storage:

- macOS: Login Keychain service `baryon-mcp`; the launcher contains no secrets
- Linux: separate mode-600 files under `$XDG_CONFIG_HOME/baryon-mcp` (default `~/.config/baryon-mcp`)
- Windows: current-user DPAPI encryption under `%LOCALAPPDATA%\baryon-mcp`

### Manual setup and other clients

Download and extract the platform archive from the [latest release](https://github.com/combor/baryon-mcp/releases/latest), then point your client at the `baryon-mcp` binary using the configuration below.

Releases provide a universal macOS build, Linux builds for amd64 and arm64, and a Windows amd64 build.

## Configuration

The installers prompt for these values. For manual setup, in Proton Mail Bridge:

1. Copy the IMAP username and Bridge-generated password from the mailbox settings. Do not use your Proton account password.
2. Export `cert.pem` from **Settings → Advanced settings → Export TLS certificates**.

Add the standalone binary to your MCP client, adapting the surrounding config format if needed:

Manual client configuration may store these values as plaintext. Prefer the installer-generated launcher when using Claude Code or Codex.

```json
{
  "mcpServers": {
    "baryon": {
      "command": "/absolute/path/to/baryon-mcp",
      "env": {
        "PROTON_BRIDGE_USERNAME": "you@proton.me",
        "PROTON_BRIDGE_PASSWORD": "bridge-generated-password",
        "PROTON_BRIDGE_TLS_CERT": "/absolute/path/to/cert.pem"
      }
    }
  }
}
```

| Environment variable | Default | Description |
|---|---|---|
| `PROTON_BRIDGE_USERNAME` | — | Required Bridge IMAP username |
| `PROTON_BRIDGE_PASSWORD` | — | Required Bridge-generated password |
| `PROTON_BRIDGE_HOST` | `127.0.0.1` | Bridge host; loopback addresses only |
| `PROTON_BRIDGE_IMAP_PORT` | `1143` | Bridge IMAP port |
| `PROTON_BRIDGE_IMAP_SECURITY` | `starttls` | `starttls` or `tls` |
| `PROTON_BRIDGE_TLS_CERT` | auto-detect | Path to Bridge's exported certificate |
| `PROTON_BRIDGE_ALLOW_INSECURE` | `false` | Disable certificate verification; see Security |
| `BARYON_ATTACHMENT_ROOTS` | unrestricted | Directories (path-list separated) that `save_draft` `content_path` may read from |

Without an explicit or auto-discovered certificate, Baryon refuses to start unless `PROTON_BRIDGE_ALLOW_INSECURE=true`.

## Usage

For reading mail:

1. Call `list_folders`.
2. Call `list_emails` or `search_emails`.
3. Pass the returned `folder`, `uid`, and `uidvalidity` to `get_email` or the attachment tools.

For drafts, omit `uid` and `uidvalidity` to create one. To replace an existing draft, pass both values and submit the complete desired state. Read the current draft with `get_email` and fetch any attachments first so recipients, bodies, and files can be retained.

Each attachment supplies its content in exactly one of two ways: `content_base64` (inline bytes, with `filename` and `content_type` required) or `content_path` (an absolute path to a regular file on the machine running Baryon, read when the draft is saved; `filename` defaults to the path's basename and `content_type` is inferred from the extension). All attachments are read and validated before anything touches the mailbox, so a missing or unreadable file fails the call without creating or replacing a draft.

A replacement gets a new UID. Baryon appends it before removing the previous draft and returns a warning if cleanup is incomplete. Drafts with genuine reply-thread `In-Reply-To` or `References` metadata are refused because Bridge cannot preserve it through IMAP replacement.

Draft limits:

- 50,000 characters each for plain-text and HTML bodies
- 100 regular attachments
- 25 MB decoded per attachment and in total, across both content sources
- Generated RFC822/MIME message below 70 MiB
- Standard base64 for inline content; inline CID attachments are not supported
- `content_path` is not available on Windows (resolving attacker-planted junctions could leak SMB credentials); use `content_base64` there

## Security

- Baryon refuses to send Bridge credentials to a non-loopback host.
- Bridge's TLS certificate is pinned by default. Insecure mode allows a local process to impersonate Bridge and capture its generated password.
- Read tools select mailboxes read-only and do not mark messages as read.
- `save_draft` is the only mutating tool. There are no send, move, general delete, or flag-changing tools.
- `save_draft` `content_path` reads local files with the server's privileges. It refuses anything but regular files (resolving symlinks first), and `BARYON_ATTACHMENT_ROOTS` optionally restricts which directories it may read; unset means any file your user account can read.
- MCP clients can access message content and attachments; connect only clients you trust.

## Development

```sh
make build      # build ./baryon-mcp
make test       # formatting, vet, and race-enabled tests
make snapshot   # local GoReleaser build and MCPB packaging into dist/
```

`make snapshot` also requires GoReleaser, `jq`, and `npx`.

## License

[BSD 3-Clause](LICENSE)
