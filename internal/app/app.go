// Package app wires the brain, store, and index together and exposes the
// operations the CLI (and later the MCP server) drive.
package app

import (
	"os"
	"path/filepath"

	"bbrain/internal/brain"
	"bbrain/internal/fact"
	"bbrain/internal/index"
	"bbrain/internal/store"
)

// App is the high-level façade over one brain.
type App struct {
	Store *store.Store
	Brain brain.Brain
}

// New builds an App rooted at a brain directory.
func New(root string) *App {
	b := brain.New(root)
	return &App{Store: store.New(b), Brain: b}
}

// ensureIndexDir creates the directory that holds the derived index, so the
// index can be opened for writing even on a freshly cleaned brain.
func (a *App) ensureIndexDir() error {
	return os.MkdirAll(filepath.Dir(a.Brain.IndexPath()), 0755)
}

// Init creates the brain structure and builds an initial (empty) index.
func (a *App) Init() error {
	if err := a.Brain.Init(); err != nil {
		return err
	}
	_, err := a.Reindex()
	return err
}

// Reindex rebuilds the FTS index from the .md files on disk and returns how many
// facts were indexed. The index is fully derived: it is cleared first.
func (a *App) Reindex() (int, error) {
	facts, err := a.Store.ListFacts()
	if err != nil {
		return 0, err
	}
	if err := a.ensureIndexDir(); err != nil {
		return 0, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return 0, err
	}
	defer ix.Close()
	if err := ix.Clear(); err != nil {
		return 0, err
	}
	for _, f := range facts {
		if err := ix.IndexFact(f, a.Store.PathFor(f)); err != nil {
			return 0, err
		}
	}
	return len(facts), nil
}

// Save persists a fact and incrementally indexes it.
func (a *App) Save(in store.SaveInput) (fact.Fact, error) {
	f, err := a.Store.Save(in)
	if err != nil {
		return fact.Fact{}, err
	}
	if err := a.ensureIndexDir(); err != nil {
		return fact.Fact{}, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return fact.Fact{}, err
	}
	defer ix.Close()
	if err := ix.IndexFact(f, a.Store.PathFor(f)); err != nil {
		return fact.Fact{}, err
	}
	return f, nil
}

// Search runs a lexical search over the index.
func (a *App) Search(query string, limit int) ([]index.Result, error) {
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return nil, err
	}
	defer ix.Close()
	return ix.Search(query, limit)
}
