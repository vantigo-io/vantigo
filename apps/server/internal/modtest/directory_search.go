package modtest

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// FakeDirectorySearch is a contracts.DirectorySearch a test shapes, for
// identity's directory endpoints to be tested without composing Entra beside
// them (Google Workspace design G8). Users answers the accounts set by
// SetUsers whose display name, mail or principal name starts with the
// search, ignoring case, at most limit; Matches answers what SetMatches set.
// FailWith makes the next calls of a method answer an error instead.
//
// It is safe for concurrent use.
type FakeDirectorySearch struct {
	mu          sync.Mutex
	key         string
	displayName string
	users       []contracts.DirectoryUser
	matches     contracts.DirectoryMatches
	fail        map[string]error
	calls       []DirectorySearchCall
}

// DirectorySearchCall is one call a FakeDirectorySearch recorded: the
// method, and for Users the search and the limit.
type DirectorySearchCall struct {
	Method string
	Search string
	Limit  int
}

var _ contracts.DirectorySearch = (*FakeDirectorySearch)(nil)

// NewFakeDirectorySearch returns an empty FakeDirectorySearch under the key
// "entra", displayed "Microsoft Entra ID".
func NewFakeDirectorySearch() *FakeDirectorySearch {
	return &FakeDirectorySearch{key: "entra", displayName: "Microsoft Entra ID", fail: map[string]error{}}
}

// WithKey makes Key answer key and DisplayName name. It answers the fake,
// for a constructor's chain.
func (f *FakeDirectorySearch) WithKey(key, name string) *FakeDirectorySearch {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.key, f.displayName = key, name
	return f
}

// Key is the provider's key, "entra" unless WithKey set another.
func (f *FakeDirectorySearch) Key() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.key
}

// DisplayName is the directory's name, "Microsoft Entra ID" unless WithKey
// set another.
func (f *FakeDirectorySearch) DisplayName() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.displayName
}

// SetUsers replaces the accounts Users searches.
func (f *FakeDirectorySearch) SetUsers(users ...contracts.DirectoryUser) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users = slices.Clone(users)
}

// SetMatches replaces what Matches answers.
func (f *FakeDirectorySearch) SetMatches(m contracts.DirectoryMatches) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.matches = m
}

// FailWith makes every later call of method ("Users", "Matches") answer
// err; nil clears it.
func (f *FakeDirectorySearch) FailWith(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.fail, method)
		return
	}
	f.fail[method] = err
}

// Calls is every call made so far, in order.
func (f *FakeDirectorySearch) Calls() []DirectorySearchCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Users answers the accounts whose display name, mail or principal name
// starts with search, ignoring case, at most limit.
func (f *FakeDirectorySearch) Users(_ context.Context, search string, limit int) ([]contracts.DirectoryUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, DirectorySearchCall{Method: "Users", Search: search, Limit: limit})
	if err := f.fail["Users"]; err != nil {
		return nil, err
	}
	s := strings.ToLower(search)
	var out []contracts.DirectoryUser
	for _, u := range f.users {
		if len(out) >= limit {
			break
		}
		for _, v := range []string{u.DisplayName, u.Mail, u.PrincipalName} {
			if v != "" && strings.HasPrefix(strings.ToLower(v), s) {
				out = append(out, u)
				break
			}
		}
	}
	return out, nil
}

// Matches answers what SetMatches set.
func (f *FakeDirectorySearch) Matches(context.Context) (contracts.DirectoryMatches, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, DirectorySearchCall{Method: "Matches"})
	if err := f.fail["Matches"]; err != nil {
		return contracts.DirectoryMatches{}, err
	}
	m := f.matches
	m.Matches = slices.Clone(m.Matches)
	return m, nil
}
