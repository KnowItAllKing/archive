# Design

## Motivation

Agent-harness auto-memory is overpowering: it leaks into context when not
wanted. This system is the anti-memory — a **pull-based** knowledge store.
Nothing is ever ambiently injected into a session. Knowledge enters only when
the user explicitly invokes the archive skill, or when the agent runs a
silent, targeted search because a question unambiguously refers to stored
personal knowledge. Harness memory is disabled, not migrated.

## Decisions

- **Files are canonical, the database is a cache.** Markdown entries with YAML
  frontmatter are the source of truth. The SQLite FTS5 index is derived,
  gitignored, and rebuilt from files whenever missing or stale.
- **Two repos.** This repo is the tool. `~/archive-store` holds the knowledge
  and is its own Git repo; its history is the audit trail. Every mutation
  auto-commits there; pushes are always manual.
- **The CLI is dumb; agents do the thinking.** Extraction, distillation,
  categorization, and match-or-create all happen in agent sessions following
  the contract printed by `archive prompt`. The CLI stays mechanical and
  offline, so the intelligence upgrades with the models at zero code change.
- **No API-key billing by default.** All LLM work runs through subscription
  CLIs (claude, codex, ...). Embeddings default to a local pure-Go model;
  pointing `ARCHIVE_EMBEDDINGS` at a remote OpenAI-compatible endpoint (LM
  Studio, an API) is an explicit per-machine opt-in.
- **Hybrid search, no vector database.** FTS5 BM25 (OR-matching, title/tags
  boosted) fused with cosine similarity via reciprocal rank fusion. At the
  expected scale (low thousands of entries), cosine is brute-forced in Go over
  a persistent embedding cache keyed by model and content hash — no sqlite-vec,
  no cgo. Entries still carry synonym tags so short human queries land.
- **Living wiki, not a log.** Ingest searches for prior art and updates the
  one authoritative entry per topic; Git history preserves its evolution.
- **Curated taxonomy.** Agents must pick from `categories.yaml` or file under
  `inbox/` for later gardening. They never mint categories.
- **Distilled + smart raw.** The distillate is canonical; a source pointer is
  always kept; raw text is stashed only when the source is ephemeral.
- **Versioned layers.** The index schema is stamped with `PRAGMA user_version`
  and rebuilt on mismatch — never migrated. The durable store format is
  versioned in `store.yaml` and upgraded by `archive migrate`'s ordered,
  one-way steps, each committed to the store's history.
- **Skill distribution via fleet.** The `archive` skill lives in
  `~/fleet/skills/archive/` and fleet links it into every harness path. The
  skill is a thin wrapper: the canonical contract is `archive prompt`,
  embedded in the binary.
