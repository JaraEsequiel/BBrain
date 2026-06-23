// Package store writes and reads facts as .md files under a brain's raws/facts/.
// The files are the source of truth; this package never caches them elsewhere.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/natefinch/atomic"

	"bbrain/internal/brain"
	"bbrain/internal/fact"
)

// dedupeWindow is how long an exact-duplicate save is folded into the existing
// file instead of creating a new one.
const dedupeWindow = 15 * time.Minute

// Store persists facts for one brain.
type Store struct {
	Brain brain.Brain
	Now   func() time.Time
}

// New returns a Store with Now defaulting to time.Now.
func New(b brain.Brain) *Store {
	return &Store{Brain: b, Now: time.Now}
}

// SaveInput is the data needed to create or upsert a fact.
type SaveInput struct {
	Type     string
	Title    string
	Body     string
	Project  string
	Scope    string
	TopicKey string
	Tags     []string
}

// PathFor returns the .md path for a fact.
func (s *Store) PathFor(f fact.Fact) string {
	return filepath.Join(s.Brain.FactsDir(), f.ID+".md")
}

// Save writes a fact to disk. With a TopicKey it upserts the matching file
// (bumping RevisionCount); otherwise it creates a new file. Exact duplicates
// saved within dedupeWindow are skipped (the existing file is returned).
func (s *Store) Save(in SaveInput) (fact.Fact, error) {
	now := s.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	existing, err := s.ListFacts()
	if err != nil {
		return fact.Fact{}, err
	}

	// Topic-key upsert: rewrite the existing file in place.
	if in.TopicKey != "" {
		for _, e := range existing {
			if e.TopicKey == in.TopicKey && e.Project == in.Project && e.Scope == in.Scope {
				e.Type = in.Type
				e.Title = in.Title
				e.Body = in.Body
				e.Tags = in.Tags
				e.UpdatedAt = nowStr
				e.RevisionCount++
				if err := s.write(e); err != nil {
					return fact.Fact{}, err
				}
				return e, nil
			}
		}
	}

	// Exact-duplicate guard within the rolling window.
	h := contentHash(in)
	for _, e := range existing {
		if contentHashOf(e) == h {
			if t, perr := time.Parse(time.RFC3339, e.UpdatedAt); perr == nil {
				if now.Sub(t.UTC()) <= dedupeWindow {
					return e, nil
				}
			}
		}
	}

	f := fact.Fact{
		ID:            fact.NewID(now.Format("2006-01-02"), in.Title),
		Type:          in.Type,
		Scope:         in.Scope,
		Project:       in.Project,
		TopicKey:      in.TopicKey,
		Tags:          in.Tags,
		CreatedAt:     nowStr,
		UpdatedAt:     nowStr,
		RevisionCount: 1,
		Title:         in.Title,
		Body:          in.Body,
	}
	if err := s.write(f); err != nil {
		return fact.Fact{}, err
	}
	return f, nil
}

func (s *Store) write(f fact.Fact) error {
	if err := os.MkdirAll(s.Brain.FactsDir(), 0o755); err != nil {
		return err
	}
	return atomic.WriteFile(s.PathFor(f), strings.NewReader(fact.Marshal(f)))
}

// ListFacts parses every .md file directly under the brain's facts dir.
func (s *Store) ListFacts() ([]fact.Fact, error) {
	dir := s.Brain.FactsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []fact.Fact
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		f, err := fact.Parse(string(data))
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

func contentHash(in SaveInput) string {
	return hashParts(in.Project, in.Scope, in.Type, in.Title, in.Body)
}

func contentHashOf(f fact.Fact) string {
	return hashParts(f.Project, f.Scope, f.Type, f.Title, f.Body)
}

func hashParts(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
