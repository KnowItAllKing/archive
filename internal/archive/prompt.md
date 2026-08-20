# Archive instructions

`archive` is a pull-based local knowledge store. Markdown files in the archive store are the source of truth. The SQLite database is a disposable search index.

## Ingest

1. Search before writing: `archive search --json "terms that describe the topic"`.
2. If one result is a strong topical match, read it with `archive show ID`, then update that entry. Keep one authoritative, living entry per topic. Otherwise add a new entry.
3. Distill the source into conclusions, facts, decisions, and procedures. Do not save a transcript as the canonical body.
4. Run `archive categories --json`. Choose a listed category. If none fits, use the literal category `inbox`. Never invent a category.
5. Set `--source` to the URL, file path, or session reference for every entry.
6. Use `--raw` only for ephemeral sources such as conversations or links likely to disappear. Durable sources do not need a raw copy.
7. Add lowercase kebab-case tags. Include retrieval synonyms that a short human query may use.
8. If the knowledge is perishable — an unverified claim, a version-specific fact, a decision pending confirmation — set `--review YYYY-MM-DD` for when it should be re-verified. `archive list --due-review` surfaces overdue entries.
9. Link related entries by writing `[[entry-id]]` in the body. `archive related --json ID` lists the nearest entries by embedding similarity; use it to find link targets and to spot near-duplicates that should be merged instead of added.

For a new entry, pass the distilled Markdown body on stdin or with `--file`:

```sh
archive add --title "..." --category infra --tags "auth,keycloak,terraform" --source "..." --file distilled.md
```

For a strong topical match, preserve the useful existing material while replacing the body with the updated distillate:

```sh
archive update --title "..." --tags "auth,keycloak,terraform" --source "..." --file distilled.md ENTRY_ID
```

To change only metadata (tags, title, category, source, review) without touching the body, pass `--keep-body` instead of a body.

Entries tagged `jot` are raw captured thoughts awaiting distillation (`archive list --tag jot`). Distilling one means rewriting it as a proper entry: real title, distilled body, category, tags without `jot`.

## Retrieval

Archive retrieval is pull-based. Never bulk-load the store or inject it into every session.

A silent, targeted `archive search --json` is allowed only when the user's question clearly looks like it may refer to stored personal knowledge, prior decisions, project history, preferences, or procedures. Use a result only when it is clearly on-topic. If results are weak or unrelated, ignore them.

Archive retrieval is local. Never browse the web as part of archive lookup.
