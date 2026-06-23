// Package index is BBrain's derived, disposable search index. It mirrors the
// .md facts into a SQLite FTS5 table for fast lexical (BM25) search and can be
// rebuilt from disk at any time.
package index

import (
	"database/sql"
	"strings"

	_ "modernc.org/sqlite"

	"bbrain/internal/fact"
)

// Index wraps a SQLite connection holding the FTS5 facts table.
type Index struct {
	db *sql.DB
}

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
	project UNINDEXED
);`

// Open opens (or creates) the index at path. Use ":memory:" for tests.
func Open(path string) (*Index, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Index{db: db}, nil
}

// Close closes the underlying database.
func (ix *Index) Close() error { return ix.db.Close() }

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
		`INSERT INTO facts_fts (fact_id, path, title, body, tags, topic_key, type, scope, project)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, path, f.Title, f.Body, strings.Join(f.Tags, " "), f.TopicKey,
		f.Type, f.Scope, f.Project,
	); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Clear empties the index (used before a full reindex).
func (ix *Index) Clear() error {
	_, err := ix.db.Exec(`DELETE FROM facts_fts`)
	return err
}

// Result is one search hit.
type Result struct {
	FactID  string
	Title   string
	Type    string
	Project string
	Path    string
}

// Search runs an FTS5 MATCH over title/body/tags/topic_key, ranked by BM25.
func (ix *Index) Search(query string, limit int) ([]Result, error) {
	match := buildMatch(query)
	if match == "" {
		return nil, nil
	}
	rows, err := ix.db.Query(
		`SELECT fact_id, title, type, project, path
		 FROM facts_fts
		 WHERE facts_fts MATCH ?
		 ORDER BY rank
		 LIMIT ?`, match, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.FactID, &r.Title, &r.Type, &r.Project, &r.Path); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// buildMatch turns a raw user query into a safe FTS5 expression: each whitespace
// token is wrapped in double quotes (with internal quotes doubled), so FTS5
// special characters are treated as literals and tokens are AND-ed together.
func buildMatch(q string) string {
	fields := strings.Fields(q)
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ReplaceAll(f, `"`, `""`)
		quoted = append(quoted, `"`+f+`"`)
	}
	return strings.Join(quoted, " ")
}
