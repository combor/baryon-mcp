# Graph Report - baryon-mcp  (2026-07-19)

## Corpus Check
- 40 files · ~23,256 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 489 nodes · 956 edges · 20 communities (18 shown, 2 thin omitted)
- Extraction: 88% EXTRACTED · 12% INFERRED · 0% AMBIGUOUS · INFERRED: 113 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `7d4b788a`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- Read Tool Registration
- Bridge Data Models
- Manifest Metadata
- Manifest Configuration
- Draft Save Pipeline
- MIME Parsing
- CI and Repository Guidance
- Draft Protocol Tests
- Content Protocol Tests
- MCP Content Tests
- Runtime Client Setup
- Draft Tool and Tests
- Runtime Configuration
- Product Documentation
- Client Session Tests
- MCPB Packaging
- Context Type
- Draft Type
- Repository Root

## God Nodes (most connected - your core abstractions)
1. `newTestSession()` - 23 edges
2. `callTool()` - 18 edges
3. `startMemServerWithOptions()` - 17 edges
4. `fakeBridge` - 15 edges
5. `die()` - 15 edges
6. `buildDraftMessage()` - 14 edges
7. `main()` - 14 edges
8. `New()` - 13 edges
9. `Load()` - 13 edges
10. `Walk()` - 12 edges

## Surprising Connections (you probably didn't know these)
- `Fail-before Pass-after Verification Loop` --semantically_similar_to--> `Go Formatting Vetting and Race Tests`  [INFERRED] [semantically similar]
  AGENTS.md → .github/workflows/ci.yml
- `main()` --calls--> `RegisterAll()`  [INFERRED]
  cmd/baryon-mcp/main.go → internal/mcptools/register.go
- `GoReleaser Action` --references--> `GoReleaser Release Configuration`  [INFERRED]
  .github/workflows/ci.yml → .goreleaser.yml
- `main()` --calls--> `New()`  [INFERRED]
  cmd/baryon-mcp/main.go → internal/bridgeclient/client.go
- `main()` --calls--> `Load()`  [INFERRED]
  cmd/baryon-mcp/main.go → internal/config/config.go

## Import Cycles
- None detected.

## Hyperedges (group relationships)
- **Email Reading Flow** — readme_list_folders, readme_list_emails, readme_search_emails, readme_get_email [EXTRACTED 0.90]
- **Attachment Management** — readme_list_attachments, readme_get_attachment, readme_get_email [EXTRACTED 0.85]
- **Repository Change Principles** — agents_focused_changes, agents_evidence_backed_review, agents_think_before_coding, agents_simplicity_first, agents_surgical_changes, agents_goal_driven_execution [EXTRACTED 1.00]

## Communities (20 total, 2 thin omitted)

### Community 0 - "Read Tool Registration"
Cohesion: 0.08
Nodes (36): Bridge, Server, registerGetAttachment(), registerListAttachments(), Server, registerGetEmail(), toAttachmentMetas(), fetchPage() (+28 more)

### Community 1 - "Bridge Data Models"
Cohesion: 0.11
Nodes (23): AttachmentContent, AttachmentInfo, EmailContent, EmailSummary, Folder, MessagePage, SavedDraft, SearchCriteria (+15 more)

### Community 2 - "Manifest Metadata"
Cohesion: 0.05
Nodes (38): author, name, compatibility, platforms, description, display_name, PROTON_BRIDGE_ALLOW_INSECURE, PROTON_BRIDGE_HOST (+30 more)

### Community 3 - "Manifest Configuration"
Cohesion: 0.05
Nodes (38): default, description, title, type, default, description, title, type (+30 more)

### Community 4 - "Draft Save Pipeline"
Cohesion: 0.11
Nodes (35): Draft, draftAddresses, draftMetadata, observedDoneContext, Header, InlineHeader, InlineWriter, appendDraft() (+27 more)

### Community 5 - "MIME Parsing"
Cohesion: 0.13
Nodes (32): BodyStructure, cleanBase64(), DecodeBinary(), decodeCTE(), DecodeText(), newBase64Cleaner(), sanitizeUTF8(), toUTF8() (+24 more)

### Community 6 - "CI and Repository Guidance"
Cohesion: 0.09
Nodes (31): Repository Checkout Action, CI Workflow, Go Formatting Vetting and Race Tests, GoReleaser Action, Go Vulnerability Check Action, Vulnerability Check Job, MCPB Manifest Validation, Release Job (+23 more)

### Community 7 - "Draft Protocol Tests"
Cohesion: 0.12
Nodes (36): AppendOptions, CapSet, draftMessageID(), AppendData, Client, T, seedDraftMailbox(), TestProtocolSavedDraftMaximumAttachmentIsReadable() (+28 more)

### Community 8 - "Content Protocol Tests"
Cohesion: 0.45
Nodes (11): Client, T, liveRef(), multipartMessage(), seedContentInbox(), TestProtocolAttachmentRoundtrip(), TestProtocolAttachmentSizeCapRefusal(), TestProtocolGetEmail() (+3 more)

### Community 9 - "MCP Content Tests"
Cohesion: 0.11
Nodes (41): T, msgRefArgs(), TestGetAttachmentImageContent(), TestGetAttachmentTextBase64(), TestGetEmailBodiesInContentOnly(), TestGetEmailNoTextBodies(), TestGetEmailRequiresUIDValidity(), TestListAttachmentsTool() (+33 more)

### Community 10 - "Runtime Client Setup"
Cohesion: 0.30
Nodes (14): buildTLSConfig(), certProbePaths(), Certificate, parsePEMCertificates(), pinnedTLSConfig(), T, selfSignedCert(), TestExplicitCertBeatsProbe() (+6 more)

### Community 11 - "Draft Tool and Tests"
Cohesion: 0.18
Nodes (27): append_client(), cleanup_and_exit(), configure_claude(), configure_client(), configure_clients(), configure_codex(), die(), download_release() (+19 more)

### Community 12 - "Runtime Configuration"
Cohesion: 0.11
Nodes (29): DraftAttachment, DraftRef, draftTestSession, tlsConfigHolder, main(), Config, Security, ExpungeWriter (+21 more)

### Community 13 - "Product Documentation"
Cohesion: 0.22
Nodes (11): IMAP, Model Context Protocol (MCP), Proton Mail Bridge, Baryon MCP, get_attachment, get_email, list_attachments, list_emails (+3 more)

### Community 14 - "Client Session Tests"
Cohesion: 0.62
Nodes (6): Client, T, stallingServer(), TestCancellationReleasesSlot(), testClient(), TestStalledHandshakeReleasesSlot()

### Community 16 - "Context Type"
Cohesion: 0.28
Nodes (15): Assert-Windows(), Find-Certificate(), Get-ClientAdapters(), Get-ExpectedHash(), Get-Release(), Get-WindowsPowerShellPath(), Install-Certificate(), Install-Credentials() (+7 more)

### Community 17 - "Draft Type"
Cohesion: 0.43
Nodes (4): fail(), run_installer(), run_macos_installer(), install_test.sh script

## Knowledge Gaps
- **80 isolated node(s):** `github.com/combor/baryon-mcp`, `Client`, `Client`, `getAttachmentOutput`, `listEmailsInput` (+75 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **2 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `New()` connect `Runtime Configuration` to `MCP Content Tests`, `Runtime Client Setup`, `Draft Protocol Tests`?**
  _High betweenness centrality (0.156) - this node is a cross-community bridge._
- **Why does `fakeBridge` connect `Bridge Data Models` to `MCP Content Tests`, `Draft Save Pipeline`?**
  _High betweenness centrality (0.120) - this node is a cross-community bridge._
- **Why does `Draft` connect `Draft Save Pipeline` to `MCP Content Tests`, `Runtime Configuration`, `Bridge Data Models`?**
  _High betweenness centrality (0.099) - this node is a cross-community bridge._
- **Are the 16 inferred relationships involving `newTestSession()` (e.g. with `TestGetAttachmentImageContent()` and `TestGetAttachmentTextBase64()`) actually correct?**
  _`newTestSession()` has 16 INFERRED edges - model-reasoned connections that need verification._
- **Are the 9 inferred relationships involving `callTool()` (e.g. with `TestGetAttachmentImageContent()` and `TestGetAttachmentTextBase64()`) actually correct?**
  _`callTool()` has 9 INFERRED edges - model-reasoned connections that need verification._
- **Are the 9 inferred relationships involving `startMemServerWithOptions()` (e.g. with `seedDraftMailbox()` and `TestProtocolSaveDraftAppendFailurePreservesPreviousDraft()`) actually correct?**
  _`startMemServerWithOptions()` has 9 INFERRED edges - model-reasoned connections that need verification._
- **What connects `github.com/combor/baryon-mcp`, `Client`, `Client` to the rest of the system?**
  _80 weakly-connected nodes found - possible documentation gaps or missing edges._