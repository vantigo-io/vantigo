package assistant

import (
	"context"
	"sync"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/assistant/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// The levels of a permission (S16).
const (
	levelOff    = "off"
	levelRead   = "read"
	levelChange = "change"
)

// levelsRecheck is how long the gate trusts the version it last read before it
// asks the database again: a change made on this replica takes effect at once
// (invalidate), one made on another within this window, at the next tool call
// after it.
const levelsRecheck = time.Second

// levelTable is the workspace's levels at one settings version: the sensitive
// switch and the Owner's overrides. It is immutable once built.
type levelTable struct {
	version   int32
	sensitive string
	overrides map[string]string
}

// defaultLevel is a permission's level when the Owner has not set one (S16): a
// permission that is not sensitive is Read and change, and a sensitive one
// takes the switch's value.
func (t *levelTable) defaultLevel(p contracts.Permission) string {
	if p.Sensitive {
		if t.sensitive == sensitiveRead {
			return levelRead
		}
		return levelOff
	}
	return levelChange
}

// effective is a permission's level: the override, else the default.
func (t *levelTable) effective(p contracts.Permission) string {
	if level, ok := t.overrides[p.Key]; ok {
		return level
	}
	return t.defaultLevel(p)
}

// levelCache keeps the current levelTable, cached by the settings' version
// (S16: a change takes effect at the next tool call).
type levelCache struct {
	mu        sync.Mutex
	table     *levelTable
	checkedAt time.Time
}

// invalidate forgets the table, so the next gate call reads the database.
func (c *levelCache) invalidate() {
	c.mu.Lock()
	c.table, c.checkedAt = nil, time.Time{}
	c.mu.Unlock()
}

// get is the current table. It asks the database for the settings row at most
// once per levelsRecheck, and reads the overrides only when the version moved.
// An error leaves the previous table in place only for the recheck window; a
// caller with none gets the error and must fail closed.
func (s *server) levelTable(ctx context.Context) (*levelTable, error) {
	c := &s.levels
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.table != nil && time.Since(c.checkedAt) < levelsRecheck {
		return c.table, nil
	}
	q := store.New(s.deps.Pool)
	row, err := q.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	if c.table != nil && c.table.version == row.Version {
		c.checkedAt = time.Now()
		return c.table, nil
	}
	levels, err := q.ListPermissionLevels(ctx)
	if err != nil {
		return nil, err
	}
	table := &levelTable{version: row.Version, sensitive: row.SensitiveDefault, overrides: make(map[string]string, len(levels))}
	for _, l := range levels {
		table.overrides[l.PermissionKey] = l.Level
	}
	c.table, c.checkedAt = table, time.Now()
	return table, nil
}

// gate is the dispatcher's gate (S16). It starts from the contract's mode and
// lets each permission the operation's access rule names narrow it: Off makes
// it never, Read turns confirm into never, Read and change leaves it. It only
// narrows — the person still needs the permission themselves and the router
// still checks every call — and it fails closed: an operation that names a
// permission the catalog lacks, or a table that cannot be read, is never.
func (s *server) gate(ctx context.Context, info operations.Info) operations.Mode {
	mode := info.Mode
	if mode == operations.ModeNever || len(info.Permissions) == 0 {
		return mode
	}
	table, err := s.levelTable(ctx)
	if err != nil {
		return operations.ModeNever
	}
	for _, key := range info.Permissions {
		perm, ok := s.deps.Catalog[key]
		if !ok {
			return operations.ModeNever
		}
		switch table.effective(perm) {
		case levelOff:
			return operations.ModeNever
		case levelRead:
			if mode == operations.ModeConfirm {
				mode = operations.ModeNever
			}
		}
	}
	return mode
}
