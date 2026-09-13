package communications

import (
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// Communications is the one module in this project with two coexisting
// error vocabularies, both observable (communications inventory §3, design
// doc §3): every module error other than stats is
// {"error":{"code","message","fields"?}} on application/json
// (CommunicationErrorResponse, EP/CommunicationEndpointHelpers.cs:31-32);
// the three stats endpoints alone answer RFC 7807 ProblemDetails on
// application/problem+json. There is no HttpValidationProblemDetails
// anywhere, and every 404 is bare-bodied. Later tasks that implement each
// area grow the CommunicationErrorResponse and ProblemDetails builders this
// file does not yet need; nothing here may invent a third shape.

// writeDecodeError is the generated server's answer to a parameter or body it
// cannot decode, before any handler — and therefore before either of the
// module's own error vocabularies — is reached. It is a bare RFC 7807
// problem, the same wire-level convention identity, customers, products and
// energy all use for this same failure mode, and it never echoes the
// decoder's error, which would leak the shape of the request it failed to
// parse.
func writeDecodeError(w http.ResponseWriter, r *http.Request) {
	httpx.WriteProblem(w, r, http.StatusBadRequest, "")
}
