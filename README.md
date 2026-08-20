# archive

`archive` is a pull-based personal knowledge store. It keeps canonical entries as Markdown in a separate Git repository and builds a disposable SQLite FTS5 index for local search.

Nothing loads the store into an agent session by default. Run `archive prompt` for the ingest and retrieval contract used by agent integrations.

## Install

```sh
go install ./cmd/archive
go build -o ~/bin/archive ./cmd/archive
```

The store defaults to `~/archive-store`. Set `ARCHIVE_STORE` to use another path.

## Start a store

```sh
archive init --remote git@github.com:you/archive-store.git
archive categories
```

`init` creates the entry, inbox, raw-source, and index directories, initializes Git, commits the starter category list, and (with `--remote`) wires up `origin`. The `.index` directory is ignored by Git.

On a machine where the store already exists remotely, clone instead of init:

```sh
git clone git@github.com:you/archive-store.git ~/archive-store
```

The first search rebuilds the index automatically; no other setup is needed.

## Add, find, and update knowledge

```sh
archive add \
  --title "Keycloak GCIP client scope" \
  --category infra \
  --tags "keycloak,auth,gcip,client-secret" \
  --source "session:example" \
  --file distilled.md

archive search --json "client secret keycloak auth"
archive update --tags "keycloak,auth,gcip,terraform,client-secret" --file revised.md ENTRY_ID
```

Search is hybrid by default: BM25 over FTS5 and cosine similarity over embeddings, fused with reciprocal rank fusion. `--lexical` and `--semantic` force a single ranker. Lexical terms use OR matching, rank title and tags above body text, and go through the `porter unicode61` tokenizer, so stems and punctuation-separated identifiers are searchable.

`update` replaces the body from stdin or `--file`. To change only metadata, pass `--keep-body`. Empty bodies are rejected on both `add` and `update`.

Run `archive help` for all commands. Read commands accept `--json`.

## Data model

```text
archive-store/
  entries/<category>/<id>.md
  inbox/<id>.md
  raw/<id>.md
  categories.yaml
  store.yaml
  .index/archive.db
```

Files are the source of truth. The index is a disposable cache: delete `.index/archive.db` at any time and the next search rebuilds it. `add` and `update` create one local Git commit each.

## Sync

Commits are automatic; pushing is always explicit:

```sh
archive status
archive push
```

`status` reports entry counts per category, the store format, uncommitted hand edits (for example from Obsidian), and how many commits the remote is missing. `push` pushes the current branch to `origin` and is the only command that ever contacts the network.

## Embeddings

By default entries are embedded with a local model (`sentence-transformers/all-MiniLM-L6-v2`), downloaded once to the user cache directory and run in pure Go — no external services, no API billing. Environment variables configure the backend:

- `ARCHIVE_EMBEDDINGS` — `local` (default), `off`, or an OpenAI-compatible endpoint URL such as `http://localhost:1234/v1` for LM Studio (`/embeddings` is appended when missing).
- `ARCHIVE_EMBEDDINGS_MODEL` — model name; optional locally, required for remote endpoints.
- `ARCHIVE_EMBEDDINGS_API_KEY` — optional bearer token for remote endpoints.

Vectors live in `.index/embeddings.db`, keyed by model and content hash. Unlike the FTS index this cache is persistent: reindexing embeds only new or changed content and prunes vectors whose content is gone. Ranking is brute-force cosine in Go — at personal-archive scale that is a few milliseconds, so there is no vector-database dependency. `archive status` reports the active backend and coverage.

## Doctor

```sh
archive doctor                # claude, model fable, effort high
archive doctor codex --model gpt-5.2 --effort medium
```

`doctor` opens an interactive session with an agent CLI (`claude` or `codex`), primed with an embedded upkeep prompt plus a live status report. The agent runs routine maintenance on its own — status, reindex, migrate, push — and asks before touching content: committing hand edits, gardening inbox entries into categories, or merging near-duplicates.

## Upgrades

The two layers version independently:

- **Index schema.** Each index is stamped with `PRAGMA user_version`. When a new binary changes the schema, the next search sees the mismatch and rebuilds the index from the Markdown files. There are no SQL migrations because the database holds no canonical state.
- **Store format.** `store.yaml` records the durable format version of the Markdown entries and layout. A binary refuses to touch a store with a newer format, and asks you to run `archive migrate` on an older one. `archive migrate` applies registered one-way steps in order, committing each format bump to the store's Git history.

## Agent integration

`archive prompt` prints the canonical ingest and retrieval contract. The `archive` skill is a thin wrapper over it and lives in the fleet repo (`~/fleet/skills/archive/`), which links it into every configured harness path (`~/.agents/skills`, `~/.claude/skills`, `~/.cline/skills`) via `make sync`.
