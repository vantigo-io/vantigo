package customers

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file white-box tests actor.go's actorFor directly (the values_test.go
// / brreg_internal_test.go convention: package customers, not
// customers_test). Two of its cases have no HTTP path in this module today
// — no principal in ctx, and a directory that no longer knows the signed-in
// user's id — so an HTTP-level test (timeline_test.go) cannot drive them;
// actorFor is unexported, so exercising it directly also requires this
// package. See customers foundation design D1 and actor.go's own comment on
// manualFallbackActor/generatedFallbackActor for why the "no principal" path
// is unreachable through this module's endpoints today.

// stubUserDirectory is a minimal contracts.UserDirectory: User answers
// exactly what the test configures, Users/SearchUsers are never called by
// actorFor and panic if that ever changes.
type stubUserDirectory struct {
	user *contracts.UserEntry
	err  error
}

func (d stubUserDirectory) User(context.Context, uuid.UUID) (*contracts.UserEntry, error) {
	return d.user, d.err
}

func (d stubUserDirectory) Users(context.Context, []uuid.UUID) ([]contracts.UserEntry, error) {
	panic("actorFor never calls Users")
}

func (d stubUserDirectory) SearchUsers(context.Context, string, int) ([]contracts.UserEntry, error) {
	panic("actorFor never calls SearchUsers")
}

// serverWithUsers builds a *server whose only wired dependency is the given
// UserDirectory — everything actorFor touches.
func serverWithUsers(d contracts.UserDirectory) *server {
	return &server{deps: module.Deps{Users: d}}
}

// TestActorFor_NoPrincipal_ReturnsFallback proves the "no user to attribute
// to" branch: no HTTP path in this module reaches it today (module.Router
// requires authentication on every operation that can write a timeline
// entry), but actor.go documents it as the designed-in answer for a future
// caller that can run unauthenticated.
func TestActorFor_NoPrincipal_ReturnsFallback(t *testing.T) {
	s := serverWithUsers(stubUserDirectory{})
	got, err := s.actorFor(context.Background(), manualFallbackActor)
	if err != nil {
		t.Fatalf("actorFor: %v", err)
	}
	if got != manualFallbackActor {
		t.Errorf("actorFor(no principal) = %+v, want the fallback %+v", got, manualFallbackActor)
	}
}

// TestActorFor_SCIMPrincipal_ReturnsFallback proves a SCIM-authenticated
// caller falls back too — SCIM never reaches these operations today, but the
// check fails safe if that ever changes.
func TestActorFor_SCIMPrincipal_ReturnsFallback(t *testing.T) {
	s := serverWithUsers(stubUserDirectory{})
	ctx := contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: uuid.New(), SCIM: true})
	got, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		t.Fatalf("actorFor: %v", err)
	}
	if got != generatedFallbackActor {
		t.Errorf("actorFor(SCIM) = %+v, want the fallback %+v", got, generatedFallbackActor)
	}
}

// TestActorFor_ZeroUserID_ReturnsFallback proves a Principal carrying the
// zero UUID (never a real user id) also falls back, rather than resolving
// "the zero user".
func TestActorFor_ZeroUserID_ReturnsFallback(t *testing.T) {
	s := serverWithUsers(stubUserDirectory{})
	ctx := contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: uuid.Nil})
	got, err := s.actorFor(ctx, manualFallbackActor)
	if err != nil {
		t.Fatalf("actorFor: %v", err)
	}
	if got != manualFallbackActor {
		t.Errorf("actorFor(zero UserID) = %+v, want the fallback %+v", got, manualFallbackActor)
	}
}

// TestActorFor_DirectoryError_IsReturned proves a directory failure is
// surfaced to the caller unchanged, never silently swallowed into a guess —
// "a timeline that guesses is worse than a 500" (this task's brief).
func TestActorFor_DirectoryError_IsReturned(t *testing.T) {
	wantErr := errors.New("directory unavailable")
	s := serverWithUsers(stubUserDirectory{err: wantErr})
	ctx := contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: uuid.New()})
	_, err := s.actorFor(ctx, manualFallbackActor)
	if !errors.Is(err, wantErr) {
		t.Errorf("actorFor error = %v, want it to wrap %v", err, wantErr)
	}
}

// TestActorFor_UnknownUser_IsUnknownUser proves a principal whose user the
// directory no longer knows about ((nil, nil), contracts.UserDirectory's
// "does not exist" shape) still resolves — attributed to "Unknown user",
// UserID still set to the principal's id, rather than answering an error or
// falling back to "unattributed"/"system".
func TestActorFor_UnknownUser_IsUnknownUser(t *testing.T) {
	s := serverWithUsers(stubUserDirectory{})
	userID := uuid.New()
	ctx := contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: userID})
	got, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		t.Fatalf("actorFor: %v", err)
	}
	want := actor{Kind: "user", Display: "Unknown user", UserID: &userID}
	if got.Kind != want.Kind || got.Display != want.Display || got.UserID == nil || *got.UserID != userID {
		t.Errorf("actorFor(unknown user) = %+v, want Kind=%q Display=%q UserID=%s", got, want.Kind, want.Display, userID)
	}
}

// TestActorFor_KnownUser_ResolvesDisplayName proves the ordinary path: a
// principal whose user the directory knows resolves to actor kind "user",
// that user's own display name, and the principal's id.
func TestActorFor_KnownUser_ResolvesDisplayName(t *testing.T) {
	userID := uuid.New()
	s := serverWithUsers(stubUserDirectory{user: &contracts.UserEntry{ID: userID, DisplayName: "Ada Lovelace", Active: true}})
	ctx := contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: userID})
	got, err := s.actorFor(ctx, manualFallbackActor)
	if err != nil {
		t.Fatalf("actorFor: %v", err)
	}
	if got.Kind != "user" || got.Display != "Ada Lovelace" || got.UserID == nil || *got.UserID != userID {
		t.Errorf("actorFor(known user) = %+v, want Kind=user Display=\"Ada Lovelace\" UserID=%s", got, userID)
	}
}
