package modtest

import (
	"context"
	"slices"
	"sync"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// FakeBrands is a contracts.Brands a test shapes, for a module that offers a
// brand picker or reads a brand to be tested without composing Identity beside
// it (brands design B29). Set replaces the brands it knows, SetLogo the bytes
// of a logo hash, and SetError makes every call fail. List answers the
// published ones, default first; Get answers any brand it holds. It is safe
// for concurrent use.
type FakeBrands struct {
	mu     sync.Mutex
	brands []contracts.BrandEntry
	logos  map[string]contracts.BrandLogo
	err    error
	calls  int
}

var _ contracts.Brands = (*FakeBrands)(nil)

// NewFakeBrands is a provider that knows no brand.
func NewFakeBrands() *FakeBrands { return &FakeBrands{logos: map[string]contracts.BrandLogo{}} }

// Set replaces the brands the fake holds. A brand's State defaults to
// published.
func (f *FakeBrands) Set(brands ...contracts.BrandEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.brands = slices.Clone(brands)
	for i := range f.brands {
		if f.brands[i].State == "" {
			f.brands[i].State = contracts.BrandStatePublished
		}
	}
}

// SetLogo makes LogoBytes answer logo for sha256.
func (f *FakeBrands) SetLogo(sha256 string, logo contracts.BrandLogo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logos[sha256] = logo
}

// SetError makes every call answer err (nil to answer again).
func (f *FakeBrands) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// Calls is how many calls the fake answered or refused.
func (f *FakeBrands) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// List is the published brands, the default first, then in the order Set gave.
func (f *FakeBrands) List(context.Context) ([]contracts.BrandEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := []contracts.BrandEntry{}
	for _, b := range f.brands {
		if b.State == contracts.BrandStatePublished {
			out = append(out, b)
		}
	}
	slices.SortStableFunc(out, func(a, b contracts.BrandEntry) int {
		switch {
		case a.Default && !b.Default:
			return -1
		case b.Default && !a.Default:
			return 1
		}
		return 0
	})
	return out, nil
}

// Get is the brand with id, (nil, nil) when the fake holds none.
func (f *FakeBrands) Get(_ context.Context, id uuid.UUID) (*contracts.BrandEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	for _, b := range f.brands {
		if b.ID == id {
			return &b, nil
		}
	}
	return nil, nil
}

// LogoBytes is the logo set for sha256, else contracts.ErrBrandLogoNotFound.
func (f *FakeBrands) LogoBytes(_ context.Context, sha256 string) (contracts.BrandLogo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return contracts.BrandLogo{}, f.err
	}
	if l, ok := f.logos[sha256]; ok {
		return l, nil
	}
	return contracts.BrandLogo{}, contracts.ErrBrandLogoNotFound
}
