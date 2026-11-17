package purchasing

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// A line's input tells a field left out from one sent as null (P16): a left
// out packagingId takes the supplier item's packaging, a null one is the
// variant's unit, and a line of a saved order keeps what it has for every
// field left out. The generated decoder reads both as nil, so the request's
// raw body is kept on the context (keepRawBody, Orders' strictbody.go's
// shape) and the lines' keys are read from it.

// maxKeptBody is the most of a body keepRawBody keeps: an order of 500 lines
// is well inside it.
const maxKeptBody = 2 << 20

type rawBodyKey struct{}

// keepRawBody puts a JSON request's body on the context and gives the
// decoder the same bytes.
func keepRawBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxKeptBody+1))
		_ = r.Body.Close()
		if err != nil || len(body) > maxKeptBody {
			next.ServeHTTP(w, r)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), rawBodyKey{}, body)))
	})
}

// linePresence is the keys each line of the request's lines carries, in the
// request's order; nil when the body was not kept (a Go call, as the seed's).
func linePresence(ctx context.Context) []map[string]bool {
	body, _ := ctx.Value(rawBodyKey{}).([]byte)
	if body == nil {
		return nil
	}
	var doc struct {
		Lines []map[string]json.RawMessage `json:"lines"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil
	}
	out := make([]map[string]bool, len(doc.Lines))
	for i, l := range doc.Lines {
		out[i] = map[string]bool{}
		for k := range l {
			out[i][k] = true
		}
	}
	return out
}
