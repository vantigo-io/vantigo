package staff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// This file holds the one place a request body is judged by what the
// contract does NOT name. A schema declared with additionalProperties false —
// PersonRequest, whose sealed, flag and link fields are written elsewhere
// under other permissions — refuses a property it does not name as a 400
// naming that property, rather than dropping it on the floor as the
// generated decoder does: a client that sends nationalId to PUT /people/{id}
// must learn it was not written, not believe it was.
//
// The generated decoder reads the body once, into the typed request, so the
// raw bytes are kept for the handler by keepRawBody, which every operation
// runs through after the router has authenticated, authorised and capped it.

// rawBodyKey is the context key keepRawBody stores a request's body under,
// and uploadBodyKey the mark it leaves on a body it did not read.
type (
	rawBodyKey    struct{}
	uploadBodyKey struct{}
)

// keepRawBody keeps a copy of the request's body in its context and hands the
// generated decoder an identical reader. The decoder parses JSON whatever the
// Content-Type says — a fetch with a string body sends text/plain — so every
// body is copied except an upload's: a multipart/* or application/octet-stream
// body reaches the handler untouched, never held in memory or drained here,
// and marked as unread so a closed request refuses it (unknownProperties).
// The router has already capped the body, so the copy is bounded; a read that
// fails is handed on as the same failure, which the decoder answers as it
// would have.
func keepRawBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}
		if isUpload(r.Header.Get("Content-Type")) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), uploadBodyKey{}, true)))
			return
		}
		body, err := io.ReadAll(r.Body)
		var rest io.Reader = bytes.NewReader(body)
		if err != nil {
			rest = io.MultiReader(rest, &failingReader{err: err})
		}
		r.Body = io.NopCloser(rest)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), rawBodyKey{}, body)))
	})
}

// isUpload reports whether a Content-Type is one keepRawBody leaves unread:
// multipart/* or application/octet-stream, parameters aside.
func isUpload(contentType string) bool {
	media, _, err := mime.ParseMediaType(contentType)
	return err == nil && (strings.HasPrefix(media, "multipart/") || media == "application/octet-stream")
}

// failingReader answers err, for the part of a body that could not be read.
type failingReader struct{ err error }

func (f *failingReader) Read([]byte) (int, error) { return 0, f.err }

// unknownProperties is the request body's top-level properties that the
// contract's schema does not name, sorted, each as a field error — nothing
// when the body is not a JSON object (the decoder has judged it already) or
// the schema admits any property.
func (s *server) unknownProperties(ctx context.Context, schema string) fieldErrors {
	var f fieldErrors
	if upload, _ := ctx.Value(uploadBodyKey{}).(bool); upload {
		// The decoder took this body as JSON, but keepRawBody left it unread,
		// so its properties cannot be judged: refused rather than written
		// unchecked.
		f.add("Content-Type", "This request's body is JSON; send it as application/json")
		return f
	}
	body, _ := ctx.Value(rawBodyKey{}).([]byte)
	if len(body) == 0 || s.deps.Doc == nil {
		return f
	}
	ref := s.deps.Doc.Components.Schemas[schema]
	if ref == nil || ref.Value == nil {
		return f
	}
	if admits := ref.Value.AdditionalProperties.Has; admits == nil || *admits {
		return f
	}
	var properties map[string]json.RawMessage
	if json.Unmarshal(body, &properties) != nil {
		return f
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		if _, ok := ref.Value.Properties[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		f.add(name, "This property is not one this request takes; the request was not applied")
	}
	return f
}

// personRequestSchema is the contract's name for the person's editable
// fields.
const personRequestSchema = "StaffPersonRequest"

// meRequestSchema is the contract's name for what a person edits on their
// own record (PUT /me): the private group less the birth date and gender. A
// work-group field sent there is refused by this file's check rather than
// dropped, so the person learns HR must change it.
const meRequestSchema = "StaffMeRequest"

// absenceTypeChangeSchema is the contract's name for a change of an absence
// type, which never changes the type's key, kind or unit: one sent is
// refused, so HR learns it was not written (delivery B).
const absenceTypeChangeSchema = "StaffAbsenceTypeChangeRequest"

// absenceChangeSchema is the contract's name for an absence's fields on a
// change and on one's own new absence (delivery B): a personId sent to POST
// /me/absences is refused, so the person learns it was not written.
const absenceChangeSchema = "StaffAbsenceChangeRequest"

// closedSchemas are the request schemas whose handlers refuse a property the
// contract does not name.
var closedSchemas = []string{personRequestSchema, meRequestSchema, absenceTypeChangeSchema, absenceChangeSchema}

// bodyHas reports whether the request's JSON body names property at its top
// level — for a required property the generated decoder cannot tell from
// null, such as the link's userId, where null unlinks and a body without it
// must not.
func bodyHas(ctx context.Context, property string) bool {
	body, _ := ctx.Value(rawBodyKey{}).([]byte)
	var properties map[string]json.RawMessage
	if json.Unmarshal(body, &properties) != nil {
		return false
	}
	_, ok := properties[property]
	return ok
}

// checkClosedSchemas fails the mount unless the contract has every schema of
// closedSchemas and closes each (additionalProperties false): the refusal
// reads exactly that, so a contract that lost the schema, or opened it,
// would otherwise let a sealed field be sent and silently dropped.
func checkClosedSchemas(doc *openapi3.T) error {
	if doc == nil || doc.Components == nil {
		return errors.New("staff: no contract to check the closed request schemas against")
	}
	for _, name := range closedSchemas {
		ref := doc.Components.Schemas[name]
		if ref == nil || ref.Value == nil {
			return fmt.Errorf("staff: the contract has no %s schema", name)
		}
		if admits := ref.Value.AdditionalProperties.Has; admits == nil || *admits {
			return fmt.Errorf("staff: the contract's %s must declare additionalProperties: false", name)
		}
	}
	return nil
}
