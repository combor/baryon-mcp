# Proton Bridge MCP research and implementation plan

Status: Implemented  
Research snapshot: 2026-08-16  
Implemented: 2026-08-16 — see "Implementation record" for what the work changed about this plan.

This document preserves the Proton Bridge MCP comparison and turns its findings into a decision-complete implementation plan for Baryon. It intentionally separates observed or documented capabilities from proposals. No competitor was exercised against a live Proton Bridge during this research; competitor entries below are based on their public repositories and documentation at the snapshot date.

## Research scope and boundaries

[Proton Mail Bridge](https://proton.me/mail/bridge) exposes Proton Mail to local email clients through IMAP and SMTP. The comparison therefore focuses on MCP servers that use Bridge or the local mail protocols it exposes. Calendar and first-class Proton Contacts support are not treated as Baryon gaps because Bridge does not expose those services.

Baryon's current implementation was checked against its source, tests, README, and packaging metadata. At the snapshot date it registers nine tools covering folder discovery, message search and retrieval, thread retrieval, attachment inspection/export, and draft creation/replacement. It deliberately does not send mail or perform ordinary mailbox mutations.

## Representative comparison

The feature descriptions in this table are repository-documented capabilities, not a claim that every feature was independently validated at runtime.

| Project | Documented strengths | Documented write/safety posture |
| --- | --- | --- |
| **Baryon** (this repository) | Folder listing/search, complete message and thread retrieval, attachment list/get/save, and create/replace draft. Uses pinned Bridge TLS, loopback checks, read-only mailbox selection, UIDVALIDITY validation, and no persistent mail index. | Draft-only writes; no sending, and no flag, move, or deletion tools. Replacing a draft is the one exception: it marks the previous draft deleted and expunges that UID inside Drafts, after the replacement is safely appended. |
| [ProtonBound](https://github.com/dazzle-blip/code-protonbound) | Scoped, thread-oriented workflows; fenced/Markdown content; new, reply, and update draft helpers. | SMTP and triage are opt-in; deny-first policy and no mail cache are documented. |
| [Considus proton-bridge-mcp](https://github.com/Considus/proton-bridge-mcp) | Cross-folder search, threads, poll/ack workflows, PDF/image handling, and create/update/delete/reply/forward draft workflows. | Sending, labels, moves, and bulk operations are gated; modes, dry-run behavior, recipient provenance, and no hard deletion are documented. |
| [miketigerblue proton-bridge-mcp](https://github.com/miketigerblue/proton-bridge-mcp) | Compact tool surface for reading, search, attachments, drafts, sending, flags, moves, and deletion, with a macOS-oriented setup. | Documents TOFU certificate pinning, macOS Keychain storage, and an `acknowledged=true` parameter on consequential actions. Its hash pinning is of dependencies, not of those actions. |
| [DreamC0der-AI proton-mail-mcp](https://github.com/DreamC0der-AI/proton-mail-mcp) | SQLite/FTS indexing and offline synchronization, plus send/reply and mailbox mutations. | Trades a persistent local mail corpus for offline search; no comparable draft-only workflow was found. |
| [sethbang proton-mail-mcp](https://github.com/sethbang/proton-mail-mcp) | Rich filters, threads, attachments, fenced bodies, save/replace drafts, reply/forward, and bulk mailbox operations. | Documents read-only/dry-run controls; sending uses a hybrid/direct Proton SMTP path rather than staying entirely within a Bridge-only read/draft boundary. |
| [googlarz proton-mail-bridge-client](https://github.com/googlarz/proton-mail-bridge-client) | Very broad surface (documented as 95 tools), local indexing, thread/triage workflows, IDLE, and extensive draft/template/scheduling workflows. | Provides safety toggles, but its documentation enables write/send behavior by default. |
| [mailpouch](https://github.com/chandshy/mailpouch) | Multi-account support, optional FTS, extensive draft/scheduling/reminder workflows, and broad mailbox writes (documented as up to 86 tools). | Documents read-only defaults, human grants, and an OAuth-protected HTTP mode. |

These projects optimize for different goals. The largest tool surfaces are not automatically suitable for Baryon: local full-text indexes increase data-at-rest exposure, while send/move/delete tools materially expand the impact of model mistakes or prompt injection. Baryon's narrow, local, draft-only boundary is a meaningful feature to preserve.

## Baryon assessment

### Existing strengths to preserve

- Draft-only writes give the user a review point before delivery.
- Bridge TLS pinning and loopback restrictions constrain the network trust boundary.
- Read-only mailbox selection avoids marking messages read as a retrieval side effect.
- UIDVALIDITY checks protect UID-based operations from silently targeting a replaced mailbox.
- Attachments can be returned without maintaining a persistent local message index.
- The small tool inventory is easier to audit than a general-purpose mail administration API.

### Confirmed high-value gaps

1. **Reply correctness.** `get_email` does not expose canonical Sender and Reply-To address lists, and generic `save_draft` requires callers to reconstruct recipients and threading headers. That makes standards-correct replies unnecessarily fragile.
2. **Fail-closed local file access by default.** When `BARYON_ATTACHMENT_ROOTS` is unset, attachment paths are currently limited only by the process's OS permissions. A managed default directory would make confinement the default rather than an optional hardening step.
3. **Untrusted-content provenance.** Email subjects, bodies, addresses, filenames, and attachment content are sender-controlled. Tool descriptions and responses should explicitly preserve that trust boundary for MCP clients.
4. **Optional mailbox scoping.** Operators cannot currently restrict read tools to an allowlist of Bridge folders.
5. **Stable pagination and message correlation.** Offset paging is vulnerable to mailbox changes between calls, while summary results do not expose Message-ID for lightweight correlation.

### Findings that do not justify new features yet

- Bridge's All Mail mailbox already provides the intended cross-folder discovery and thread path. No reproducible duplicate-message defect was established, so an arbitrary fan-out/deduplication engine is not proposed.
- Folder counts, richer search filters, correspondent scopes, multi-account routing, polling/IDLE, and local FTS may be useful, but the comparison did not establish that they are more important than the five gaps above.
- Sending, flags, moves, deletion, and bulk mutations conflict with the current draft-only safety model and are not proposed.
- Calendar and first-class Contacts are outside the Bridge protocol boundary.

## Product decisions for the first implementation

- Keep the release focused on reply correctness, filesystem confinement, untrusted-content labeling, folder scoping, and stable pagination.
- Keep raw text and HTML bodies byte-for-byte compatible in structured fields. Add provenance metadata and warnings rather than sanitizing or rewriting content.
- Do not quote the original body or copy original attachments when constructing a reply draft.
- Defer a dedicated forward-draft helper.
- Make the folder allowlist optional; an unset value keeps the current all-folder behavior.
- Configure sender identities explicitly and expose them through a discovery tool.
- Preserve generic `save_draft` as the escape hatch and draft-replacement mechanism.
- Do not add sending or ordinary mailbox mutations.

## Proposed public API and configuration

- Add `list_sender_identities`, returning `identities: [{address, name?, default}]`.
- Add `save_reply_draft` with source `folder`, `uid`, and `uidvalidity`; optional configured `from`; `reply_all`; caller-supplied Bcc, bodies, and attachments; and derived recipients, subject, and thread headers. It creates a new draft only.
- Extend `get_email` with canonical RFC 5322 `sender` and `reply_to` arrays.
- Extend list/search summaries with `message_id`.
- Add `before_uid` plus `uidvalidity` cursor inputs to list/search, and `next_before_uid` to outputs. Preserve `offset` for compatibility, but reject mixing it with a cursor.
- Add `content_trust: "untrusted_email"` to message, thread, attachment, and reply-derived outputs. Keep plain and HTML body values unchanged; add warnings to tool descriptions and non-colliding fences around legacy body text blocks.
- Add `BARYON_SENDER_IDENTITIES` as an RFC 5322 address list. The first entry is the default; case-insensitive duplicates are removed. If unset, use the Bridge username when it is a valid address.
- Add `BARYON_ALLOWED_FOLDERS` as one RFC 4180 CSV row. Unset permits all folders; configured names are exact except for case-insensitive `INBOX` handling. A value that parses to no names is rejected rather than read as unrestricted, matching how attachment roots already fail closed.
- Change an unset `BARYON_ATTACHMENT_ROOTS` to the managed `${XDG_CONFIG_HOME:-$HOME/.config}/baryon-mcp/attachments` directory. Explicit roots replace that default.
- Permit relative attachment paths, resolved under the first active root; absolute paths must remain inside an active root. Preserve root pinning, no-overwrite, regular-file, symlink, size, and Windows restrictions.

The registered tool count becomes eleven. Introspection expectations and MCPB metadata must be updated with the implementation.

## Implementation plan

### 1. Preserve the research baseline

Use this document as the dated baseline for the work. When implementation evidence changes a proposed schema or assumption, update the relevant section and record the reason instead of silently drifting from the plan.

**Verify:** the comparison still distinguishes documented competitor behavior from live-tested behavior, and each implementation decision traces to a confirmed gap above.

### 2. Add configuration and enforce policy

- Parse and validate sender identities and folder scopes at startup; treat unresolved MCPB template values as unset.
- Create the managed attachment directory and set it to mode `0700` before pinning it; fail startup if this cannot be done. Explicit custom roots must already exist.
- Enforce folder scope inside the Bridge client before opening/selecting a disallowed mailbox. Filter `list_folders`; validate source and search folders for message, body, attachment, and thread operations. Draft saving remains outside the read policy.
- Propagate the non-secret policy variables through native setup and MCPB configuration without changing credential storage. Setup output should report the active managed attachment directory.

**Verify:** focused configuration and filesystem tests cover valid values, malformed values, defaults, unresolved templates, permissions, and denial before a disallowed mailbox is selected.

### 3. Implement reply correctness

- Map IMAP Envelope `Sender` and `ReplyTo` fields without an additional fetch.
- Canonically format addresses so quoted commas and non-ASCII display names remain round-trippable.
- Select `from` from configured identities: use an explicit valid choice; otherwise an identity found in the source To/Cc/Bcc; otherwise a matching sent-message From; otherwise the configured default.
- Derive primary recipients from Reply-To, then From, then Sender. Remove every configured self identity and de-duplicate by mailbox.
- For reply-all, add original To/Cc to Cc after excluding self and To duplicates. Never copy original Bcc; retain only caller-supplied Bcc.
- Preserve an existing case-insensitive `Re:` prefix or prepend `Re: `. Require a valid parent Message-ID.
- Set In-Reply-To to the parent Message-ID. Build References from parent References, or parent In-Reply-To when References is absent, then append the parent once within existing limits.
- Do not quote the original body or copy its attachments automatically. Keep generic `save_draft` for custom construction and replacement.

**Verify:** table-driven reply tests cover Reply-To precedence, Sender fallback, identity selection, reply versus reply-all, self-alias removal, duplicates, Bcc privacy, subject handling, missing Message-ID, bounded References, stale UIDVALIDITY, bodies/attachments, and Bridge save failures.

### 4. Add stable discovery and untrusted provenance

- Introduce an internal page request carrying limit, legacy offset, and an optional UID cursor.
- After read-only `SELECT`, validate cursor UIDVALIDITY, search and sort descending, restrict results below `before_uid`, and page them.
- Calculate `next_before_uid` from the selected UID boundary rather than the last successfully fetched message, avoiding races with expunges. Keep `total` as all criteria matches before pagination.
- Continue to use All Mail as the canonical cross-folder search/thread mechanism. Do not add arbitrary folder fan-out or Message-ID deduplication without a reproducible Bridge case.
- Apply the trust marker consistently to sender-controlled subjects, addresses, bodies, filenames, attachment bytes/images, and reply-derived fields. Describe it as defense in depth, not proof that content is safe.

**Verify:** pagination tests cover insertion between pages, stale/partial cursors, cursor/offset conflicts, end-of-results, and expunge between search and fetch. Provenance tests cover every affected structured response, warning-bearing descriptions, unchanged bodies, and legacy fences that sender content cannot close.

### 5. Update documentation and packaging

- Update README workflows, capability and security tables, tool schemas, configuration examples, migration guidance, and the managed-directory path.
- Update `manifest.json` descriptions, user configuration, and the eleven-tool inventory. Do not change release versions or generated package hashes as part of this implementation.
- Explain that existing users relying on unrestricted absolute paths must configure an explicit attachment root. Docker's existing `/attachments` root remains valid when explicitly configured.

**Verify:** tool introspection, README examples, native setup, container behavior, and MCPB validation agree on names, schemas, defaults, and tool count.

## Complete test plan

- **Configuration:** identity parsing, default selection, de-duplication, malformed input, CSV folder names including quoted commas, unset scopes, managed-root precedence, unresolved templates, and startup failures.
- **Filesystem:** managed-root reads/writes, safe relative paths, absolute in-root paths, traversal/outside-root rejection, explicit root replacement, symlinks, no overwrite, permissions, and unchanged Windows behavior.
- **Reply:** Reply-To precedence, Sender fallback, identity selection, reply/reply-all, self aliases, duplicate recipients, Bcc privacy, `Re:` handling, missing Message-ID, bounded References, stale UIDVALIDITY, caller bodies/attachments, and Bridge save failures.
- **Provenance:** trust marker on every relevant structured response, warnings in tool descriptions, unchanged body values, and non-colliding legacy fences.
- **Pagination:** append between pages without duplicate/skip, stale and partial cursor rejection, cursor/offset conflict, end-of-results, and expunge between search and fetch.
- **Folder scope:** filtered folder listing and early denial for list/search/get/thread/attachment operations; All Mail works only when allowed; draft writes remain available.
- **Integration:** Sender/Reply-To and Message-ID envelope mapping, explicit All Mail search/thread behavior, eleven-tool introspection, legacy MCP revisions, container introspection, and MCPB validation.
- **Final checks:** run `make test`, the repository's manifest validation, and a live-Bridge smoke test that creates and inspects a reply draft without sending it.

## Deferred work and safety conditions

Forward-draft helpers, folder counts, richer filters, correspondent scopes, multi-account routing, polling/IDLE, FTS, scheduling, HTTP transport, sending, and ordinary mailbox mutations are deferred. Raw HTML remains available as today and is labeled untrusted rather than sanitized.

If sending is proposed later, it requires a separate design review. At minimum, authorization should be bound to the exact human-reviewed draft content and recipients, rather than inferred from a prior general approval.

## Success criteria

The proposed release is complete only when all five confirmed gaps are covered by focused tests, the public schemas and packaging agree, compatibility behavior is documented, and a live Bridge smoke test confirms that a standards-correct reply draft is created but never sent.

## Implementation record

Written while implementing the plan above. Each entry is a place where the code differs from what the plan assumed, and why.

### Decisions the plan left open

- **Where the managed attachment directory is created.** Not in `config.Load`. `internal/setup` runs `Load` only to resolve the Bridge endpoint, so creating and chmod-ing directories there would make an unrelated command leave a filesystem trace. `Load` resolves the path into `ManagedAttachmentRoot`; `Config.ActivateManagedAttachmentRoot` creates it at mode 0700 and appends it to `AttachmentRoots`, and only `serve` calls that. Setup reports the path it will use without creating it.
- **Windows has no managed root.** Both `content_path` and `save_attachment` refuse to run on Windows before touching any path, so a mandatory directory there would only add a startup failure guarding a boundary nothing can cross. `managedAttachmentRoot` returns empty on Windows and the tools stay refused.
- **"A matching sent-message From" means the source message's own From.** Read literally it could mean searching Sent for a related message; that would cost an extra mailbox scan per reply. It is implemented as: if the message being answered carries a configured identity in From, reply from that identity. It applies when such a message still names an address to reply to — a message this account sent under one identity with a Reply-To pointing elsewhere, say. It selects the From only; it never decides recipients.
- **Recipients come only from Reply-To, From and Sender, exactly as the plan says.** An implementation attempt to also fall back to the parent's To — so that replying to a message the user sent would continue with its recipients — was withdrawn under review. Headers cannot establish that a message really came from this account: any sender may put a configured identity in From, and the fallback then addressed the reply to whichever third party that message listed in To, while the response reported the recipients as derived from the origin. A message naming no origin but this account is refused, pointing at `save_draft`, where the caller names recipients itself. The identity rule above still applies whenever such a message does name a reply address.
- **Cursor inputs pair like the draft ones.** `before_uid` and `uidvalidity` must be supplied together or not at all, matching `save_draft`'s existing uid/uidvalidity rule. Accepting `uidvalidity` alone would have been a third, differently-shaped validity check for no gain.

### What the work changed about the plan

- **Canonical address formatting fixes a present-day defect, not only the reply path.** `formatAddresses` emitted `Name <addr>` unquoted while `save_draft` parses recipients with `net/mail`, so a display name containing a comma — `Doe, John` — already could not be round-tripped: copying `from` out of `get_email` into `save_draft` failed for such senders. The fix therefore lands in `bridgeclient`, is shared by every tool through the exported `FormatAddress`, and is covered by its own round-trip test rather than only by reply tests. Non-ASCII names stay readable instead of becoming RFC 2047 encoded-words, which also round-trip but are unreadable in a listing.
- **`save_reply_draft` reuses `GetEmail` rather than adding a Bridge method.** It costs one body fetch the reply does not use, bounded by the existing text caps, and keeps the reply logic a pure function over an already-tested surface. Splitting a headers-only fetch out of `GetEmail` would be worth it only if reply drafts became hot.
- **No fence for `get_thread` on legacy peers.** It returns nil content and lets the SDK serialize its structured output, which already carries `content_trust`. Only `get_email` and `get_attachment` build text blocks by hand, so only they fence.
- **`Message-ID` values are stripped of angle brackets wherever the envelope supplies them.** `EmailSummary.MessageID` and the envelope fallback in `threadHeaders.fillFromEnvelope` both go through `bareMsgID`, so one message reports one identifier no matter which path read it.
- **The trust marker skips `list_folders` and `list_sender_identities`.** Both report the account's own configuration rather than anything a sender wrote, and marking them would dilute what the marker means.

### Verification

`make test` (gofmt, registry manifest tests, vet, `go test -race ./...`) and `make docker-smoke` pass, the latter confirming the eleven-tool inventory through container introspection. `npx @anthropic-ai/mcpb validate manifest.json` passes. The live-Bridge smoke test in the plan's final checks is not automatable here and remains for the maintainer to run against a real Bridge.
