package products

import (
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// This module's refusals are bare RFC 7807 problems and field-level validation
// text, never identity's {code, message} bodies: .NET's Products endpoints
// used TypedResults.Problem and TypedResults.ValidationProblem throughout and
// have no machine-readable code vocabulary at all, so callers key off the
// status and the problem's title and detail (products inventory §1). Nothing
// here may grow an error code.

// writeDecodeError is the generated server's answer to a parameter or body it
// cannot decode: a bare 400 problem. It never echoes the decoder's error,
// which would leak the shape of the request it failed to parse.
func writeDecodeError(w http.ResponseWriter, r *http.Request) {
	httpx.WriteProblem(w, r, http.StatusBadRequest, "")
}
