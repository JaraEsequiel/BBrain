// Package index is BBrain's derived, disposable search index. It mirrors the
// .md facts into a SQLite FTS5 table for fast lexical (BM25) search and can be
// rebuilt from disk at any time.
package index

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/JaraEsequiel/BBrain/internal/fact"
)

// Index wraps a SQLite connection holding the FTS5 facts table.
type Index struct {
	db    *sql.DB
	stale bool
}

// indexSchemaVersion is bumped whenever facts_fts's schema changes in a way
// that requires a reindex (e.g. a tokenizer change). Stamped via PRAGMA
// user_version in Reset(); checked in Open() to detect a stale, un-reindexed
// on-disk index (see isStale).
const indexSchemaVersion = 1

// snippetTokens is the FTS5 snippet() token budget approximating the ~160-char
// preview cap. ponytail: tuned by eye against real fact bodies during design;
// adjust here if previews read too short/long in practice — no other code depends on the exact
// value.
const snippetTokens = 28

// schema: a single standalone FTS5 table. Searchable columns (title, body, tags,
// topic_key) are tokenized; identifiers/filters (fact_id, path, type, scope,
// project) are UNINDEXED so they are stored verbatim and usable in WHERE.
const schema = `
CREATE VIRTUAL TABLE IF NOT EXISTS facts_fts USING fts5(
	fact_id UNINDEXED,
	path UNINDEXED,
	title,
	body,
	tags,
	topic_key,
	type UNINDEXED,
	scope UNINDEXED,
	project UNINDEXED,
	updated_at UNINDEXED,
	created_at UNINDEXED,
	tokenize = 'porter unicode61'
);`

// linksSchema is a plain (non-FTS) table mirroring each fact's reasoned wikilinks.
// Like facts_fts it is derived from the .md and rebuilt on reindex. Targets are
// stored as bare fact ids (the [[ ]] wrapping is stripped on the way in).
const linksSchema = `
CREATE TABLE IF NOT EXISTS links (
	src_id   TEXT NOT NULL,
	dst_id   TEXT NOT NULL,
	relation TEXT NOT NULL,
	why      TEXT NOT NULL,
	PRIMARY KEY (src_id, dst_id, relation)
);`

// Open opens (or creates) the index at path. Use ":memory:" for tests.
func Open(path string) (*Index, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	for _, stmt := range []string{schema, linksSchema} {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, err
		}
	}
	stale, err := isStale(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Index{db: db, stale: stale}, nil
}

// isStale reports whether facts_fts holds content indexed under a schema
// older than indexSchemaVersion. A version mismatch on an empty table means
// "not yet reindexed since creation" — nothing stale to warn about — not
// staleness; only a mismatch on a non-empty table is a real signal.
func isStale(db *sql.DB) (bool, error) {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return false, err
	}
	if version >= indexSchemaVersion {
		return false, nil
	}
	var hasRows bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM facts_fts LIMIT 1)`).Scan(&hasRows); err != nil {
		return false, err
	}
	return hasRows, nil
}

// Close closes the underlying database.
func (ix *Index) Close() error { return ix.db.Close() }

// Stale reports whether this Index was opened against an on-disk facts_fts
// table indexed under an older schema (e.g. pre-porter tokenizer) that
// hasn't been rebuilt via Reset()/bbrain reindex yet.
func (ix *Index) Stale() bool {
	return ix.stale
}

// IndexFact upserts a fact: it removes any existing row with the same fact_id
// then inserts the current content.
func (ix *Index) IndexFact(f fact.Fact, path string) error {
	tx, err := ix.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM facts_fts WHERE fact_id = ?`, f.ID); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO facts_fts (fact_id, path, title, body, tags, topic_key, type, scope, project, updated_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, path, f.Title, f.Body, strings.Join(f.Tags, " "), f.TopicKey,
		f.Type, f.Scope, f.Project, f.UpdatedAt, f.CreatedAt,
	); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// LastSavedAt returns the most recent updated_at among facts in project, and
// whether any exist. The timestamp is the raw RFC3339 string stored on the
// fact. project is matched exactly (a global/empty-project fact does not count).
func (ix *Index) LastSavedAt(project string) (string, bool, error) {
	var ts sql.NullString
	err := ix.db.QueryRow(
		`SELECT max(updated_at) FROM facts_fts WHERE project = ?`, project,
	).Scan(&ts)
	if err != nil {
		return "", false, err
	}
	if !ts.Valid || ts.String == "" {
		return "", false, nil
	}
	return ts.String, true, nil
}

// IndexLinks mirrors a fact's reasoned wikilinks into the links table: it removes
// any existing edges originating from f.ID, then inserts one row per link. Targets
// are normalized to bare fact ids; empty targets are skipped. Dangling edges (a
// dst_id with no indexed fact) are allowed — they still answer graph queries.
func (ix *Index) IndexLinks(f fact.Fact) error {
	tx, err := ix.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM links WHERE src_id = ?`, f.ID); err != nil {
		tx.Rollback()
		return err
	}
	for _, l := range f.Links {
		dst := fact.LinkTargetID(l.Target)
		if dst == "" {
			continue
		}
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO links (src_id, dst_id, relation, why) VALUES (?, ?, ?, ?)`,
			f.ID, dst, l.Relation, l.Why,
		); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Edge is one reasoned graph edge.
type Edge struct {
	SrcID    string `json:"src_id"`
	DstID    string `json:"dst_id"`
	Relation string `json:"relation"`
	Why      string `json:"why"`
}

// Neighbor is a fact connected to a given fact, with the relation, its why, and
// the direction relative to the queried fact ("out": this fact links to FactID;
// "in": FactID links to this fact).
type Neighbor struct {
	FactID    string `json:"fact_id"`
	Relation  string `json:"relation"`
	Why       string `json:"why"`
	Direction string `json:"direction"`
}

// Why returns the reasoned edges directly connecting a and b, in either direction
// — this answers "why is A related to B". Empty when there is no direct link.
func (ix *Index) Why(aID, bID string) ([]Edge, error) {
	rows, err := ix.db.Query(
		`SELECT src_id, dst_id, relation, why FROM links
		 WHERE (src_id = ? AND dst_id = ?) OR (src_id = ? AND dst_id = ?)
		 ORDER BY src_id, dst_id, relation`,
		aID, bID, bID, aID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.SrcID, &e.DstID, &e.Relation, &e.Why); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Neighbors returns every fact linked to or from id, with direction. Out-edges
// (id -> X) come first, then in-edges (Y -> id), each ordered by the other fact's
// id for deterministic output.
func (ix *Index) Neighbors(id string) ([]Neighbor, error) {
	rows, err := ix.db.Query(
		`SELECT dst_id, relation, why, 'out' AS dir FROM links WHERE src_id = ?
		 UNION ALL
		 SELECT src_id, relation, why, 'in' AS dir FROM links WHERE dst_id = ?
		 ORDER BY 4, 1`,
		id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Neighbor
	for rows.Next() {
		var n Neighbor
		if err := rows.Scan(&n.FactID, &n.Relation, &n.Why, &n.Direction); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Reset drops and recreates the derived tables so a schema change in `schema`
// takes effect. Unlike a row-wipe, this migrates the table definition; callers
// repopulate via IndexFact. The index is derived from the .md files, so dropping
// it loses nothing.
func (ix *Index) Reset() error {
	tx, err := ix.db.Begin()
	if err != nil {
		return err
	}
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS facts_fts`,
		`DROP TABLE IF EXISTS links`,
		schema, linksSchema,
		fmt.Sprintf(`PRAGMA user_version = %d`, indexSchemaVersion),
	} {
		if _, err := tx.Exec(stmt); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	ix.stale = false
	return nil
}

// Result is one search hit.
type Result struct {
	FactID  string `json:"fact_id"`
	Title   string `json:"title"`
	Type    string `json:"type"`
	Project string `json:"project"`
	Path    string `json:"path"`
	Snippet string `json:"snippet"`
}

// Search runs an FTS5 MATCH over title/body/tags/topic_key (all query terms
// AND-ed), ranked by BM25. project/type, when non-empty, restrict results to an
// exact match on those columns (both empty preserves the unfiltered behavior).
func (ix *Index) Search(query string, limit int, project, typ string) ([]Result, error) {
	return ix.search(buildMatch(query), limit, project, typ)
}

// SearchAny is like Search but matches facts containing ANY of the query terms
// (OR semantics), ranked by BM25. It powers candidate/correlation discovery, where
// a strict AND would miss facts that only partially overlap.
func (ix *Index) SearchAny(query string, limit int, project, typ string) ([]Result, error) {
	return ix.search(buildMatchAny(query), limit, project, typ)
}

func (ix *Index) search(match string, limit int, project, typ string) ([]Result, error) {
	if match == "" {
		return []Result{}, nil
	}
	q := `SELECT fact_id, title, type, project, path, snippet(facts_fts, 3, '', '', '...', ?)
	      FROM facts_fts
	      WHERE facts_fts MATCH ?`
	args := []any{snippetTokens, match}
	if project != "" {
		q += ` AND project = ?`
		args = append(args, project)
	}
	if typ != "" {
		q += ` AND type = ?`
		args = append(args, typ)
	}
	q += ` ORDER BY rank LIMIT ?`
	args = append(args, limit)

	rows, err := ix.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Result, 0)
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.FactID, &r.Title, &r.Type, &r.Project, &r.Path, &r.Snippet); err != nil {
			return nil, err
		}
		// The FTS5 snippet() builtin preserves the source body's raw whitespace/newlines
		// verbatim (confirmed by spike) — it does not collapse them like the ticket's
		// AC-3 requires. Normalize here; word-boundary safety (AC-2) is untouched since
		// this only folds runs of whitespace, never splits a word.
		r.Snippet = strings.Join(strings.Fields(r.Snippet), " ")
		out = append(out, r)
	}
	return out, rows.Err()
}

// quoteTokens splits q into whitespace-delimited tokens and wraps each in double
// quotes, doubling any embedded quote so FTS5 treats every token as a literal
// (neutralizing FTS special characters). A blank query yields an empty slice, so
// strings.Join over the result returns "" — the empty match the search core
// short-circuits on.
func quoteTokens(q string) []string {
	fields := strings.Fields(q)
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ReplaceAll(f, `"`, `""`)
		quoted = append(quoted, `"`+f+`"`)
	}
	return quoted
}

// buildMatch turns a raw user query into a safe FTS5 expression: each token is
// quoted via quoteTokens, then AND-ed together.
func buildMatch(q string) string {
	return strings.Join(quoteTokens(q), " ")
}

// buildMatchAny is like buildMatch but OR-joins the quoted tokens, so a fact
// matching any single term is returned.
func buildMatchAny(q string) string {
	return strings.Join(quoteTokens(q), " OR ")
}

// DeleteFact removes a fact's search row and its outgoing links from the index.
func (ix *Index) DeleteFact(id string) error {
	tx, err := ix.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM facts_fts WHERE fact_id = ?`, id); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM links WHERE src_id = ?`, id); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
