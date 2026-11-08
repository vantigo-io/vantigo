package knowledge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/knowledge/gen"
)

// This module's refusals follow the codebase's rule (knowledge design K6): an
// article the caller cannot read answers a bare 404, never 403, so its
// existence is not revealed; 403 article_read_only for one they can read but
// not write; 400 naming the field or the rule; 409 for a rule about the state
// of things; 413 for a document over 2 MiB. Every body is gen.KnowledgeProblem
// carrying the rule's code, which a client keys off, never the words.
const (
	codeMoveIntoDescendant     = "move_into_descendant"
	codeTooDeep                = "too_deep"
	codeParentInvalid          = "parent_invalid"
	codeAfterInvalid           = "after_invalid"
	codeInvalidDocument        = "invalid_document"
	codeRootPermissionRequired = "root_permission_required"
	codeLastWriter             = "last_writer"
	codeArticleReadOnly        = "article_read_only"
	codeVersionConflict        = "version_conflict"
	codeArticleLocked          = "article_locked"
	codeCollabInactive         = "collab_inactive"
	codeCollabActive           = "collab_active"
	codeVisibilityChange       = "visibility_change"
	codeNotTrashed             = "not_trashed"
	codeDocumentTooLarge       = "document_too_large"
)

// refusal is an operation's answer that is not a success, returned from
// inside its transaction so everything the write did rolls back, and turned
// into the operation's own typed response by answer. updatedBy is a
// version_conflict's author, named after the transaction (no call leaves the
// module inside one).
type refusal struct {
	status    int
	problem   gen.KnowledgeProblem
	updatedBy *uuid.UUID
}

func (r *refusal) Error() string { return fmt.Sprintf("knowledge: refused with %d", r.status) }

// problem is a problem body with a status, a code, a title and a detail; an
// empty code or detail is left out.
func problem(status int, code, title, detail string) gen.KnowledgeProblem {
	s := int32(status)
	p := gen.KnowledgeProblem{Title: &title, Status: &s}
	if code != "" {
		p.Code = &code
	}
	if detail != "" {
		p.Detail = &detail
	}
	return p
}

// notFound is the bare 404 of an article the caller cannot read (K6).
func notFound() error { return &refusal{status: http.StatusNotFound} }

// readOnly is the 403 of a write to an article the caller can only read.
func readOnly() error {
	return &refusal{status: http.StatusForbidden, problem: problem(http.StatusForbidden, codeArticleReadOnly,
		"The article is read-only for you", "You can read this article but not change it.")}
}

// fieldErrors is a validation pass's outcome: the field errors and, when one
// of them is a rule the design names, its code.
type fieldErrors struct {
	errs map[string][]string
	code string
}

func (f *fieldErrors) add(field, message string) {
	if f.errs == nil {
		f.errs = map[string][]string{}
	}
	f.errs[field] = append(f.errs[field], message)
}

func (f *fieldErrors) failed() bool { return len(f.errs) > 0 }

// err is the 400 of the failed fields, or nil.
func (f *fieldErrors) err() error {
	if !f.failed() {
		return nil
	}
	p := problem(http.StatusBadRequest, f.code, "Invalid request", "")
	p.Errors = &f.errs
	return &refusal{status: http.StatusBadRequest, problem: p}
}

// invalid is a 400 naming one field under code ("" for a plain field error).
func invalid(field, code, message string) error {
	f := fieldErrors{code: code}
	f.add(field, message)
	return f.err()
}

// conflict is a 409 with code.
func conflict(code, title, detail string) error {
	return &refusal{status: http.StatusConflict, problem: problem(http.StatusConflict, code, title, detail)}
}

// versionConflict is the 409 of a save made from a version that is not the
// current one (K9): the current version, when and by whom it was saved.
func versionConflict(current int32, updatedAt time.Time, updatedBy *uuid.UUID) error {
	p := problem(http.StatusConflict, codeVersionConflict, "Someone else changed this page",
		fmt.Sprintf("The article is at version %d. Reload it, or save again from that version.", current))
	p.CurrentVersion = &current
	at := updatedAt.UTC()
	p.UpdatedAt = &at
	return &refusal{status: http.StatusConflict, problem: p, updatedBy: updatedBy}
}

// locked is the 409 of a content edit of a locked article (K11a).
func locked() error {
	return conflict(codeArticleLocked, "The article is locked", "A writer locked this article; unlock it to change its content.")
}

// collabInactive is the 409 of a collaborative save of an article whose live
// log is empty: nobody is co-editing it (K32), so the save would race a
// single-editor one.
func collabInactive() error {
	return conflict(codeCollabInactive, "Live editing is not active", "The article has no live log; save it as a single editor.")
}

// collabActive is the 409 of a single-editor save or a restore of an article
// whose live log took a row within collabActiveWindow: someone is co-editing
// it, and the write would delete their log.
func collabActive() error {
	return conflict(codeCollabActive, "Live editing is active", "Someone is editing the article live. Try again when they are done, or reset the live log.")
}

// visibilityChange is the 409 of a change that moves an article between
// private and the workspace without confirmVisibility (K4).
func visibilityChange() error {
	return conflict(codeVisibilityChange, "This changes who can read the article",
		"The change moves the article between private and the workspace. Confirm it to go ahead.")
}

// tooLarge is the 413 of a document over 2 MiB (K8).
func tooLarge() error {
	return &refusal{status: http.StatusRequestEntityTooLarge, problem: problem(http.StatusRequestEntityTooLarge,
		codeDocumentTooLarge, "The document is too large", "A document can be at most 2 MiB.")}
}

// answers are one operation's typed responses for a refusal, by status; a
// status an operation does not declare is nil, and a refusal with it is a
// 500 — a bug in the handler, never a silent mismatch with the contract.
type answers[R any] struct {
	badRequest func(gen.KnowledgeProblem) R
	forbidden  func(gen.KnowledgeProblem) R
	notFound   func() R
	conflict   func(gen.KnowledgeProblem) R
	tooLarge   func(gen.KnowledgeProblem) R
}

// answer turns err into the operation's typed response, or returns it as the
// error it is. A version_conflict's author is named here, after the
// transaction.
func answer[R any](ctx context.Context, s *server, err error, a answers[R]) (R, error) {
	var zero R
	var r *refusal
	if !errors.As(err, &r) {
		return zero, err
	}
	p := r.problem
	if r.updatedBy != nil {
		p.UpdatedBy = s.actor(ctx, r.updatedBy)
	}
	switch {
	case r.status == http.StatusBadRequest && a.badRequest != nil:
		return a.badRequest(p), nil
	case r.status == http.StatusForbidden && a.forbidden != nil:
		return a.forbidden(p), nil
	case r.status == http.StatusNotFound && a.notFound != nil:
		return a.notFound(), nil
	case r.status == http.StatusConflict && a.conflict != nil:
		return a.conflict(p), nil
	case r.status == http.StatusRequestEntityTooLarge && a.tooLarge != nil:
		return a.tooLarge(p), nil
	}
	return zero, fmt.Errorf("knowledge: a refusal with status %d this operation does not declare: %w", r.status, err)
}

// noRows reports whether err is a read that found no row.
func noRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
