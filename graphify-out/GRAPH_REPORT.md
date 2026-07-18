# Graph Report - /Users/piotrkomborski/src/github.com/combor/baryon-mcp  (2026-07-18)

## Corpus Check
- 4 files · ~18,779 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 462 nodes · 839 edges · 30 communities (16 shown, 14 thin omitted)
- Extraction: 88% EXTRACTED · 12% INFERRED · 0% AMBIGUOUS · INFERRED: 98 edges (avg confidence: 0.81)
- Token cost: 0 input · 0 output

## Community Hubs (Navigation)
- Read Tool Registration
- Bridge Data Models
- Manifest Metadata
- Manifest Configuration
- Draft Save Pipeline
- MIME Parsing
- Draft Protocol Tests
- Content Protocol Tests
- MCP Content Tests
- CI Release Pipeline
- Runtime Client Setup
- Product Documentation
- Draft Tool and Tests
- Runtime Configuration
- Repository Guidance
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
1. `baryon-mcp` - 19 edges
2. `newTestSession()` - 19 edges
3. `callTool()` - 15 edges
4. `fakeBridge` - 15 edges
5. `buildDraftMessage()` - 14 edges
6. `Load()` - 13 edges
7. `Walk()` - 12 edges
8. `RegisterAll()` - 12 edges
9. `buildTLSConfig()` - 11 edges
10. `Bridge` - 10 edges

## Surprising Connections (you probably didn't know these)
- `Fail-before Pass-after Verification Loop` --semantically_similar_to--> `Go Formatting Vetting and Race Tests`  [INFERRED] [semantically similar]
  AGENTS.md → .github/workflows/ci.yml
- `MCPB Manifest Validation` --conceptually_related_to--> `MCPB Packaging Hook`  [INFERRED]
  .github/workflows/ci.yml → .goreleaser.yml
- `GoReleaser Action` --references--> `GoReleaser Release Configuration`  [INFERRED]
  .github/workflows/ci.yml → .goreleaser.yml
- `Local Snapshot Build` --conceptually_related_to--> `GoReleaser Release Configuration`  [INFERRED]
  README.md → .goreleaser.yml
- `main()` --calls--> `New()`  [INFERRED]
  cmd/baryon-mcp/main.go → internal/bridgeclient/client.go

## Import Cycles
- None detected.

## Hyperedges (group relationships)
- **Repository Change Principles** — agents_focused_changes, agents_evidence_backed_review, agents_think_before_coding, agents_simplicity_first, agents_surgical_changes, agents_goal_driven_execution [EXTRACTED 1.00]
- **Baryon MCP Mail Tool Surface** — readme_list_folders, readme_list_emails, readme_search_emails, readme_get_email, readme_list_attachments, readme_get_attachment, readme_save_draft [EXTRACTED 1.00]
- **Tag-to-release Artifact Pipeline** — readme_tag_driven_release, _github_workflows_ci_release_job, _github_workflows_ci_goreleaser_action, _goreleaser_release_configuration, _goreleaser_cross_platform_build, _goreleaser_mcpb_pack_hook, _goreleaser_release_archives, _goreleaser_sha256_checksums, _goreleaser_github_release_assets [INFERRED 0.95]

## Communities (30 total, 14 thin omitted)

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

### Community 6 - "Draft Protocol Tests"
Cohesion: 0.13
Nodes (26): AppendOptions, draftTestSession, ExpungeWriter, draftMessageID(), AppendData, seedDraftMailbox(), TestProtocolSavedDraftMaximumAttachmentIsReadable(), TestProtocolSavedDraftMaximumNonASCIIBodyIsReadable() (+18 more)

### Community 7 - "Content Protocol Tests"
Cohesion: 0.17
Nodes (27): CapSet, Client, T, liveRef(), multipartMessage(), seedContentInbox(), TestProtocolAttachmentRoundtrip(), TestProtocolAttachmentSizeCapRefusal() (+19 more)

### Community 8 - "MCP Content Tests"
Cohesion: 0.20
Nodes (24): T, msgRefArgs(), TestGetAttachmentImageContent(), TestGetAttachmentTextBase64(), TestGetEmailBodiesInContentOnly(), TestGetEmailNoTextBodies(), TestGetEmailRequiresUIDValidity(), TestListAttachmentsTool() (+16 more)

### Community 9 - "CI Release Pipeline"
Cohesion: 0.13
Nodes (24): Repository Checkout Action, CI Workflow, GoReleaser Action, Go Vulnerability Check Action, Vulnerability Check Job, MCPB Manifest Validation, Release Job, Go Setup Action (+16 more)

### Community 10 - "Runtime Client Setup"
Cohesion: 0.19
Nodes (19): tlsConfigHolder, Config, Client, Context, New(), buildTLSConfig(), certProbePaths(), Certificate (+11 more)

### Community 11 - "Product Documentation"
Cohesion: 0.12
Nodes (23): baryon-mcp, Bounded IMAP Resources, Draft-only Write Surface, Fresh IMAP Connection per Tool Call, get_attachment Tool, get_email Tool, IMAP, list_attachments Tool (+15 more)

### Community 12 - "Draft Tool and Tests"
Cohesion: 0.16
Nodes (19): Bridge, CallToolResult, Draft, registerSaveDraft(), saveDraftAnnotations(), decodeSavedDraft(), T, TestSaveDraftToolAnnotations() (+11 more)

### Community 13 - "Runtime Configuration"
Cohesion: 0.32
Nodes (15): main(), Security, isLoopback(), Load(), env(), T, TestLoadAllowInsecure(), TestLoadDefaults() (+7 more)

### Community 14 - "Repository Guidance"
Cohesion: 0.25
Nodes (11): Go Formatting Vetting and Race Tests, Explicit Ambiguity Handling, Evidence-backed Review, Small Focused Changes, Goal-driven Execution, Repository Agent Guidance, Simplicity First, Surgical Changes (+3 more)

### Community 15 - "Client Session Tests"
Cohesion: 0.62
Nodes (6): Client, T, stallingServer(), TestCancellationReleasesSlot(), testClient(), TestStalledHandshakeReleasesSlot()

## Knowledge Gaps
- **83 isolated node(s):** `Go Vulnerability Check Action`, `Baryon MCP Command Entrypoint`, `Platform Release Archives`, `SHA-256 Release Checksums`, `Repository Guidance Pointer` (+78 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **14 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `New()` connect `Runtime Client Setup` to `MCP Content Tests`, `Bridge Data Models`, `Runtime Configuration`, `Content Protocol Tests`?**
  _High betweenness centrality (0.084) - this node is a cross-community bridge._
- **Why does `RegisterAll()` connect `Read Tool Registration` to `MCP Content Tests`, `Draft Tool and Tests`, `Runtime Configuration`?**
  _High betweenness centrality (0.076) - this node is a cross-community bridge._
- **Why does `fakeBridge` connect `Bridge Data Models` to `MCP Content Tests`?**
  _High betweenness centrality (0.073) - this node is a cross-community bridge._
- **Are the 12 inferred relationships involving `newTestSession()` (e.g. with `TestGetAttachmentImageContent()` and `TestGetAttachmentTextBase64()`) actually correct?**
  _`newTestSession()` has 12 INFERRED edges - model-reasoned connections that need verification._
- **Are the 6 inferred relationships involving `callTool()` (e.g. with `TestGetAttachmentImageContent()` and `TestGetAttachmentTextBase64()`) actually correct?**
  _`callTool()` has 6 INFERRED edges - model-reasoned connections that need verification._
- **What connects `Go Vulnerability Check Action`, `Baryon MCP Command Entrypoint`, `Platform Release Archives` to the rest of the system?**
  _83 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Read Tool Registration` be split into smaller, more focused modules?**
  _Cohesion score 0.08305647840531562 - nodes in this community are weakly interconnected._