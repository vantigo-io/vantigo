package staff

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
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

// rawBodyKey is the context key keepRawBody stores a request's body under.
type rawBodyKey struct{}

// keepRawBody keeps a copy of the request's body in its context and hands the
// generated decoder an identical reader. The router has already capped the
// body, so the copy is bounded; a read that fails is handed on as the same
// failure, which the decoder answers as it would have.
func keepRawBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
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

// failingReader answers err, for the part of a body that could not be read.
type failingReader struct{ err error }

func (f *failingReader) Read([]byte) (int, error) { return 0, f.err }

// unknownProperties is the request body's top-level properties that the
// contract's schema does not name, sorted, each as a field error — nothing
// when the body is not a JSON object (the decoder has judged it already) or
// the schema admits any property.
func (s *server) unknownProperties(ctx context.Context, schema string) fieldErrors {
	var f fieldErrors
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
