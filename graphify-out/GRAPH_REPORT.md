# Graph Report - /Users/piotrkomborski/src/github.com/combor/baryon-mcp  (2026-07-18)

## Corpus Check
- 1 files · ~18,379 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 446 nodes · 811 edges · 29 communities (15 shown, 14 thin omitted)
- Extraction: 88% EXTRACTED · 12% INFERRED · 0% AMBIGUOUS · INFERRED: 94 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

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
- Address Type
- Append Data Type
- Bridge Client Type
- Time Type
- Client Reference
- IMAP Session Type
- Test Type
- MCP Server Type
- Tool Annotation Type
- Tool Result Type
- Repository Root

## God Nodes (most connected - your core abstractions)
1. `newTestSession()` - 19 edges
2. `callTool()` - 15 edges
3. `fakeBridge` - 15 edges
4. `buildDraftMessage()` - 14 edges
5. `Load()` - 13 edges
6. `Walk()` - 12 edges
7. `RegisterAll()` - 12 edges
8. `buildTLSConfig()` - 11 edges
9. `Bridge` - 10 edges
10. `startMemServerWithOptions()` - 10 edges

## Surprising Connections (you probably didn't know these)
- `Fail-before Pass-after Verification Loop` --semantically_similar_to--> `Go Formatting Vetting and Race Tests`  [INFERRED] [semantically similar]
  AGENTS.md → .github/workflows/ci.yml
- `GoReleaser Action` --references--> `GoReleaser Release Configuration`  [INFERRED]
  .github/workflows/ci.yml → .goreleaser.yml
- `main()` --calls--> `New()`  [INFERRED]
  cmd/baryon-mcp/main.go → internal/bridgeclient/client.go
- `main()` --calls--> `RegisterAll()`  [INFERRED]
  cmd/baryon-mcp/main.go → internal/mcptools/register.go
- `MCPB Manifest Validation` --conceptually_related_to--> `MCPB Packaging Hook`  [INFERRED]
  .github/workflows/ci.yml → .goreleaser.yml

## Import Cycles
- None detected.

## Hyperedges (group relationships)
- **Email Reading Flow** — readme_list_folders, readme_list_emails, readme_search_emails, readme_get_email [EXTRACTED 0.90]
- **Attachment Management** — readme_list_attachments, readme_get_attachment, readme_get_email [EXTRACTED 0.85]
- **Repository Change Principles** — agents_focused_changes, agents_evidence_backed_review, agents_think_before_coding, agents_simplicity_first, agents_surgical_changes, agents_goal_driven_execution [EXTRACTED 1.00]

## Communities (29 total, 14 thin omitted)

### Community 0 - "Read Tool Registration"
Cohesion: 0.08
Nodes (36): Bridge, Server, registerGetAttachment(), registerListAttachments(), Server, registerGetEmail(), toAttachmentMetas(), fetchPage() (+28 more)

### Community 1 - "Bridge Data Models"
Cohesion: 0.10
Nodes (26): AttachmentContent, AttachmentInfo, Draft, DraftAttachment, DraftRef, EmailContent, EmailSummary, Folder (+18 more)

### Community 2 - "Manifest Metadata"
Cohesion: 0.05
Nodes (38): author, name, compatibility, platforms, description, display_name, PROTON_BRIDGE_ALLOW_INSECURE, PROTON_BRIDGE_HOST (+30 more)

### Community 3 - "Manifest Configuration"
Cohesion: 0.05
Nodes (38): default, description, title, type, default, description, title, type (+30 more)

### Community 4 - "Draft Save Pipeline"
Cohesion: 0.12
Nodes (32): Address, AppendData, Client, draftAddresses, draftMetadata, observedDoneContext, Header, InlineHeader (+24 more)

### Community 5 - "MIME Parsing"
Cohesion: 0.13
Nodes (32): BodyStructure, cleanBase64(), DecodeBinary(), decodeCTE(), DecodeText(), newBase64Cleaner(), sanitizeUTF8(), toUTF8() (+24 more)

### Community 6 - "CI and Repository Guidance"
Cohesion: 0.09
Nodes (31): Repository Checkout Action, CI Workflow, Go Formatting Vetting and Race Tests, GoReleaser Action, Go Vulnerability Check Action, Vulnerability Check Job, MCPB Manifest Validation, Release Job (+23 more)

### Community 7 - "Draft Protocol Tests"
Cohesion: 0.13
Nodes (26): AppendOptions, draftTestSession, ExpungeWriter, draftMessageID(), AppendData, seedDraftMailbox(), TestProtocolSavedDraftMaximumAttachmentIsReadable(), TestProtocolSavedDraftMaximumNonASCIIBodyIsReadable() (+18 more)

### Community 8 - "Content Protocol Tests"
Cohesion: 0.17
Nodes (27): CapSet, Client, T, liveRef(), multipartMessage(), seedContentInbox(), TestProtocolAttachmentRoundtrip(), TestProtocolAttachmentSizeCapRefusal() (+19 more)

### Community 9 - "MCP Content Tests"
Cohesion: 0.20
Nodes (24): T, msgRefArgs(), TestGetAttachmentImageContent(), TestGetAttachmentTextBase64(), TestGetEmailBodiesInContentOnly(), TestGetEmailNoTextBodies(), TestGetEmailRequiresUIDValidity(), TestListAttachmentsTool() (+16 more)

### Community 10 - "Runtime Client Setup"
Cohesion: 0.19
Nodes (19): tlsConfigHolder, Config, Client, Context, New(), buildTLSConfig(), certProbePaths(), Certificate (+11 more)

### Community 11 - "Draft Tool and Tests"
Cohesion: 0.16
Nodes (19): Bridge, CallToolResult, Draft, registerSaveDraft(), saveDraftAnnotations(), decodeSavedDraft(), T, TestSaveDraftToolAnnotations() (+11 more)

### Community 12 - "Runtime Configuration"
Cohesion: 0.32
Nodes (15): main(), Security, isLoopback(), Load(), env(), T, TestLoadAllowInsecure(), TestLoadDefaults() (+7 more)

### Community 13 - "Product Documentation"
Cohesion: 0.22
Nodes (11): IMAP, Model Context Protocol (MCP), Proton Mail Bridge, Baryon MCP, get_attachment, get_email, list_attachments, list_emails (+3 more)

### Community 14 - "Client Session Tests"
Cohesion: 0.62
Nodes (6): Client, T, stallingServer(), TestCancellationReleasesSlot(), testClient(), TestStalledHandshakeReleasesSlot()

## Knowledge Gaps
- **79 isolated node(s):** `Go Vulnerability Check Action`, `Baryon MCP Command Entrypoint`, `Platform Release Archives`, `SHA-256 Release Checksums`, `Repository Guidance Pointer` (+74 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **14 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `New()` connect `Runtime Client Setup` to `Content Protocol Tests`, `Bridge Data Models`, `Runtime Configuration`, `MCP Content Tests`?**
  _High betweenness centrality (0.090) - this node is a cross-community bridge._
- **Why does `RegisterAll()` connect `Read Tool Registration` to `MCP Content Tests`, `Draft Tool and Tests`, `Runtime Configuration`?**
  _High betweenness centrality (0.082) - this node is a cross-community bridge._
- **Why does `fakeBridge` connect `Bridge Data Models` to `MCP Content Tests`?**
  _High betweenness centrality (0.079) - this node is a cross-community bridge._
- **Are the 12 inferred relationships involving `newTestSession()` (e.g. with `TestGetAttachmentImageContent()` and `TestGetAttachmentTextBase64()`) actually correct?**
  _`newTestSession()` has 12 INFERRED edges - model-reasoned connections that need verification._
- **Are the 6 inferred relationships involving `callTool()` (e.g. with `TestGetAttachmentImageContent()` and `TestGetAttachmentTextBase64()`) actually correct?**
  _`callTool()` has 6 INFERRED edges - model-reasoned connections that need verification._
- **Are the 3 inferred relationships involving `buildDraftMessage()` (e.g. with `TestBuildDraftMessageGeneratesMessageID()` and `TestBuildDraftMessagePlainHTMLAndAttachment()`) actually correct?**
  _`buildDraftMessage()` has 3 INFERRED edges - model-reasoned connections that need verification._
- **What connects `Go Vulnerability Check Action`, `Baryon MCP Command Entrypoint`, `Platform Release Archives` to the rest of the system?**
  _79 weakly-connected nodes found - possible documentation gaps or missing edges._