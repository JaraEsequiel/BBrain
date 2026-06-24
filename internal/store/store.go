// Package store writes and reads facts as .md files under a brain's raws/facts/.
// The files are the source of truth; this package never caches them elsewhere.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	Pinned   bool
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
				e.Pinned = in.Pinned
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
		Pinned:        in.Pinned,
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

// Get loads a single fact by id. ok is false (with a nil error) when no .md with
// that id exists under the brain's facts dir.
func (s *Store) Get(id string) (fact.Fact, bool, error) {
	if !fact.ValidID(id) {
		return fact.Fact{}, false, fmt.Errorf("store: invalid fact id %q", id)
	}
	data, err := os.ReadFile(filepath.Join(s.Brain.FactsDir(), id+".md"))
	if err != nil {
		if os.IsNotExist(err) {
			return fact.Fact{}, false, nil
		}
		return fact.Fact{}, false, err
	}
	f, err := fact.Parse(string(data))
	if err != nil {
		return fact.Fact{}, false, err
	}
	return f, true, nil
}

// AddLink adds (or updates) a reasoned wikilink from srcID to dstID on the source
// fact's .md frontmatter. Both facts must exist and relation must be in the
// controlled vocabulary; why is mandatory. If a link to dstID already exists, its
// relation and why are overwritten in place (no duplicate edge). The source .md is
// rewritten atomically and its updated_at is bumped; revision_count is left
// untouched because a link edit is not a content revision.
func (s *Store) AddLink(srcID, dstID, relation, why string) (fact.Fact, error) {
	if !fact.ValidRelation(relation) {
		return fact.Fact{}, fmt.Errorf("store: invalid relation %q", relation)
	}
	if why == "" {
		return fact.Fact{}, fmt.Errorf("store: link why is required")
	}
	src, ok, err := s.Get(srcID)
	if err != nil {
		return fact.Fact{}, err
	}
	if !ok {
		return fact.Fact{}, fmt.Errorf("store: source fact %q not found", srcID)
	}
	if _, ok, err := s.Get(dstID); err != nil {
		return fact.Fact{}, err
	} else if !ok {
		return fact.Fact{}, fmt.Errorf("store: target fact %q not found", dstID)
	}

	updated := false
	for i := range src.Links {
		if fact.LinkTargetID(src.Links[i].Target) == dstID {
			src.Links[i].Relation = relation
			src.Links[i].Why = why
			updated = true
			break
		}
	}
	if !updated {
		src.Links = append(src.Links, fact.Link{
			Target:   fact.FormatTarget(dstID),
			Relation: relation,
			Why:      why,
		})
	}
	src.UpdatedAt = s.Now().UTC().Format(time.RFC3339)
	if err := s.write(src); err != nil {
		return fact.Fact{}, err
	}
	return src, nil
}

// RemoveLink removes any reasoned wikilink from srcID to dstID on the source
// fact's .md. It is a no-op (returning the unchanged fact) when no such link
// exists. The source .md is rewritten atomically and updated_at is bumped only
// when a link was actually removed.
func (s *Store) RemoveLink(srcID, dstID string) (fact.Fact, bool, error) {
	src, ok, err := s.Get(srcID)
	if err != nil {
		return fact.Fact{}, false, err
	}
	if !ok {
		return fact.Fact{}, false, fmt.Errorf("store: source fact %q not found", srcID)
	}
	kept := make([]fact.Link, 0, len(src.Links))
	removed := false
	for _, l := range src.Links {
		if fact.LinkTargetID(l.Target) == dstID {
			removed = true
			continue
		}
		kept = append(kept, l)
	}
	if !removed {
		return src, false, nil
	}
	src.Links = kept
	src.UpdatedAt = s.Now().UTC().Format(time.RFC3339)
	if err := s.write(src); err != nil {
		return fact.Fact{}, false, err
	}
	return src, true, nil
}

func contentHash(in SaveInput) string {
	return hashParts(in.Project, in.Scope, in.Type, in.Title, in.Body, strconv.FormatBool(in.Pinned))
}

func contentHashOf(f fact.Fact) string {
	return hashParts(f.Project, f.Scope, f.Type, f.Title, f.Body, strconv.FormatBool(f.Pinned))
}

func hashParts(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// Delete removes a fact's .md file. It returns (false, nil) when no such file
// exists, so deleting an absent fact is a no-op rather than an error.
func (s *Store) Delete(id string) (bool, error) {
	if !fact.ValidID(id) {
		return false, fmt.Errorf("store: invalid fact id %q", id)
	}
	if err := os.Remove(filepath.Join(s.Brain.FactsDir(), id+".md")); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
