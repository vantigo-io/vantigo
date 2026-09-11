package identity

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// SCIM 2.0 (RFC 7643, RFC 7644) as .NET's deliberately small,
// deployment-bound protocol served it: ScimProtocolService ("SV/SCIM"
// below, apps/identity/backend/Identity.Module/Services/ScimProtocolService.cs),
// ScimProtocolEndpoints ("EA/SCIM", .../Endpoints/Auth/ScimProtocolEndpoints.cs)
// and ScimTokenService (.../Services/ScimTokenService.cs). A static bearer
// token authenticates every request as the installation's one SCIM
// connection; there is no user behind it.

// The protocol's constants (SV/SCIM:35-44).
const (
	scimBasePath        = "/api/v1/identity/scim/v2"
	scimMediaType       = "application/scim+json"
	scimUserSchema      = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimGroupSchema     = "urn:ietf:params:scim:schemas:core:2.0:Group"
	scimListSchema      = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimPatchSchema     = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	scimMaxPageSize     = 100
	scimMaxMembers      = 100
	scimMaxBodyBytes    = 256 * 1024
	scimMaxFilterLength = 512
)

// maxGroupExternalIDLength is identity.access_groups.external_id's bound,
// .NET's column (AccessGroupEntityTypeConfiguration.cs:16). .NET validated a
// group's externalId against 512 characters (SV/SCIM:752) and its database
// then refused the longer ones with a 500; here they are refused with the
// validation's own 400.
const maxGroupExternalIDLength = 256

// scimConnectionID is .NET's ScimConnection.StaticId
// (Database/Accounts/ScimConnection.cs:5), the one connection both tokens
// authenticate as. The connection table is gone (spec *SCIM 2.0*); the id
// survives in the audit facts, which named it on every SCIM event.
var scimConnectionID = uuid.MustParse("7f6b7f8a-7c7e-4c19-8f06-6c64d3b7f4d2")

// scimLockKey is the transaction advisory lock every SCIM write takes
// (AcquireScimLock): "SCIM" in ASCII.
const scimLockKey = 0x5343494d

// policyScimIngress is .NET's ScimIngressRateLimiter (EA/SCIM:94-103): 120
// requests a minute, a fixed window, keyed by the connection when the token
// is valid and by client address otherwise (SV/SCIM:558-569).
var policyScimIngress = ratelimit.Policy{Name: "ScimIngress", Limit: 120, Window: time.Minute}

// scimConnectionKey is the one bucket every authenticated SCIM request
// counts against, whichever token it presents: .NET partitioned by the
// connection id, and both tokens authenticated as the one connection, so a
// rotation's overlap shares the 120 a minute rather than doubling them.
var scimConnectionKey = "connection:" + scimConnectionID.String()

// scimError is a SCIM error answer, {schemas, status, scimType, detail} as
// application/scim+json (SV/SCIM:1203). An empty scimType is written as
// null, as .NET's 404 wrote it. It is an error, so a transaction returns it
// to roll back and the handler answers with it (scimOr).
type scimError struct {
	status   int
	scimType string
	detail   string
}

func (e scimError) Error() string {
	return fmt.Sprintf("identity: SCIM refused with %d %s", e.status, e.scimType)
}

func (e scimError) write(w http.ResponseWriter) error {
	writeScimError(w, e.status, e.scimType, e.detail)
	return nil
}

func scimInvalidValue(detail string) scimError {
	return scimError{http.StatusBadRequest, "invalidValue", detail}
}

func scimInvalidSyntax(detail string) scimError {
	return scimError{http.StatusBadRequest, "invalidSyntax", detail}
}

func scimInvalidPath(detail string) scimError {
	return scimError{http.StatusBadRequest, "invalidPath", detail}
}

func scimInvalidFilter(detail string) scimError {
	return scimError{http.StatusBadRequest, "invalidFilter", detail}
}

// The fixed SCIM answers, each .NET's.
var (
	scimNotFound             = scimError{http.StatusNotFound, "", "The requested resource was not found."}                                        // SV/SCIM:192
	scimStale                = scimError{http.StatusPreconditionFailed, "invalidVers", "The resource version is stale."}                          // :1175
	scimStaleVersion         = scimError{http.StatusPreconditionFailed, "invalidVers", "The SCIM resource version is stale."}                     // :1183, :1188
	scimProtected            = scimError{http.StatusBadRequest, "mutability", "The selected user is protected."}                                  // :161, :247
	scimExternalIDImmutable  = scimError{http.StatusConflict, "mutability", "externalId is immutable once mapped."}                               // :300
	scimUserConflict         = scimError{http.StatusConflict, "uniqueness", "The SCIM resource conflicts with an existing resource."}             // :180
	scimGroupConflict        = scimError{http.StatusConflict, "uniqueness", "The SCIM group conflicts with an existing group."}                   // :371, :403
	scimTooManyMembers       = scimError{http.StatusBadRequest, "tooMany", "A group membership mutation exceeds the safe member cap."}            // :391, :648
	scimUnknownMember        = scimInvalidValue("Every group member must be a known SCIM User from this connection.")                             // :652
	scimInvalidPaging        = scimInvalidValue("startIndex and count are invalid.")                                                              // :1055
	scimInvalidJSON          = scimInvalidSyntax("The request body is not valid JSON.")                                                           // EA/SCIM:64
	scimUnsupportedMediaType = scimError{http.StatusUnsupportedMediaType, "invalidSyntax", "SCIM request bodies must use application/scim+json."} // EA/SCIM:39
	scimTooLarge             = scimError{http.StatusRequestEntityTooLarge, "tooLarge", "The SCIM request body exceeds the 256 KiB limit."}        // EA/SCIM:44, :54
	scimRateLimited          = scimError{http.StatusTooManyRequests, "tooMany", "SCIM request rate limit exceeded."}                              // SV/SCIM:569
)

// scimOr is a handler's answer to err: the scimError in err's chain, or err
// itself, a server error. T is the operation's response interface.
func scimOr[T any](err error) (T, error) {
	var zero T
	var e scimError
	if errors.As(err, &e) {
		if answer, ok := any(e).(T); ok {
			return answer, nil
		}
	}
	return zero, err
}

// scimBearer is the bearer token r presents, read as .NET's
// AuthenticateAsync read it (SV/SCIM:541-545): the Authorization header,
// repeated lines joined with a comma as ASP.NET's StringValues joins them,
// starts "Bearer " in any case, and what follows, trimmed, is not empty and
// holds no white space.
func scimBearer(r *http.Request) (string, bool) {
	header := strings.Join(r.Header.Values("Authorization"), ",")
	if len(header) < len("Bearer ") || !strings.EqualFold(header[:len("Bearer ")], "Bearer ") {
		return "", false
	}
	token := dotnetTrim(header[len("Bearer "):])
	if token == "" || hasWhitespace(token) {
		return "", false
	}
	return token, true
}

// scimTokenValid is ScimTokenService.VerifyAsync (ScimTokenService.cs:16-36):
// SCIM is configured, and token is the current token or, while now is
// before its expiry, the previous one. Each comparison is of the SHA-256
// digests, in constant time, so it takes as long whatever the token's
// length and contents.
func (a *Access) scimTokenValid(token string, now time.Time) bool {
	cfg := a.cfg.SCIM
	if cfg == nil {
		return false
	}
	presented := sha256.Sum256([]byte(token))
	current := sameDigest(presented, cfg.Token)
	previous := cfg.PreviousToken != "" && cfg.PreviousTokenExpiresAt.After(now) && sameDigest(presented, cfg.PreviousToken)
	return current || previous
}

// sameDigest reports, in constant time, whether presented is configured's
// SHA-256.
func sameDigest(presented [sha256.Size]byte, configured string) bool {
	expected := sha256.Sum256([]byte(configured))
	return subtle.ConstantTimeCompare(presented[:], expected[:]) == 1
}

// scimInputKey is the context key of a SCIM request's scimInput.
type scimInputKey struct{}

// scimInput is what a SCIM request carried that .NET's handlers read
// themselves rather than through a typed binder: the raw query, which they
// read through HttpRequest.Query, and the body, which they read as a
// JsonElement (nil for an operation without one).
type scimInput struct {
	query string
	body  []byte
}

var errNoScimInput = errors.New("identity: no SCIM input in the request context")

// scimRequest returns the request and its scimInput, which scimIngress
// stored.
func scimRequest(ctx context.Context) (scimInput, *http.Request, error) {
	r, err := requestFrom(ctx)
	if err != nil {
		return scimInput{}, nil, err
	}
	in, ok := r.Context().Value(scimInputKey{}).(scimInput)
	if !ok {
		return scimInput{}, nil, errNoScimInput
	}
	return in, r, nil
}

// scimRoute is one SCIM operation of the contract: its ServeMux pattern and
// whether it takes a body.
type scimRoute struct {
	pattern string
	body    bool
}

// scimRoutes is every operation the contract gives the scim rule.
func scimRoutes(d module.Deps) []scimRoute {
	var routes []scimRoute
	if d.Doc == nil || d.Doc.Paths == nil {
		return nil
	}
	for path, item := range d.Doc.Paths.Map() {
		for method, op := range item.Operations() {
			if access, _ := op.Extensions["x-vantigo-access"].(string); access == "scim" {
				routes = append(routes, scimRoute{pattern: method + " " + path, body: op.RequestBody != nil})
			}
		}
	}
	slices.SortFunc(routes, func(a, b scimRoute) int { return strings.Compare(a.pattern, b.pattern) })
	return routes
}

// scimIngress fronts the SCIM operations of routes with .NET's ingress,
// before the router's access check; every other request goes straight to
// next. For a SCIM operation, in .NET's order:
//
//  1. The bearer token is verified and the request counted against
//     policyScimIngress, keyed by the one connection when the token is
//     valid (scimConnectionKey) and by client address otherwise; over the
//     limit it is 429 tooMany. .NET's endpoint filter rate-limited before
//     it authenticated or read the body (EA/SCIM:77-92, SV/SCIM:558-569).
//     A failure of the ingress itself, the limiter's store or reading the
//     body, is a SCIM 500 (scimServerError).
//  2. A request without a valid token goes on to the router, whose scim
//     rule refuses it with the SCIM 401 (Access.Reject): the contract's
//     access rule stays the authority.
//  3. An authenticated request is recorded as the SCIM heartbeat
//     (SV/SCIM:574-582).
//  4. An operation with a body requires application/scim+json (415) and at
//     most 256 KiB (413), and the body must be one JSON document (400
//     invalidSyntax), as .NET's Body helper required (EA/SCIM:36-66).
//  5. The router checks the token again, as each .NET handler
//     authenticated again (SV/SCIM:539-551).
//
// .NET's handlers read the query through HttpRequest.Query (keys in any
// case, repeated values joined with a comma) and the body as a
// JsonElement, and interpreted both themselves, answering each malformed
// input with their own scimType and detail. The generated binder and
// decoder are stricter (a repeated or white-space-padded parameter, a
// mistyped field) and would answer first with an error .NET never gave.
// So the ingress keeps the query and the body in the request context
// (scimInput), where the handlers read them, and hands the request on
// without either; it also joins repeated If-Match and X-SCIM-Meta-Version
// lines with a comma, as ASP.NET did, so the binder takes each as one
// value. Nothing the generated layer decodes for a SCIM operation can then
// fail, and writeDecodeError answers in the SCIM shape if it ever did.
func (s *server) scimIngress(routes []scimRoute, next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", next)
	for _, route := range routes {
		mux.Handle(route.pattern, s.scimOperation(route.body, next))
	}
	return mux
}

func (s *server) scimOperation(body bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, presented := scimBearer(r)
		valid := presented && s.access.scimTokenValid(token, s.deps.Clock())
		client := "ip:" + cmp.Or(httpx.ClientIP(r), "unknown")
		if valid {
			client = scimConnectionKey
		}
		d, err := s.deps.Limiter.Hit(r.Context(), policyScimIngress, client)
		if err != nil {
			s.scimServerError(w, r, err)
			return
		}
		if !d.Allowed {
			_ = scimRateLimited.write(w)
			return
		}
		if !valid {
			next.ServeHTTP(w, r)
			return
		}
		s.recordOperationalEvent(r.Context(), operationalEventScimRequest)

		in := scimInput{query: r.URL.RawQuery}
		if body {
			b, err := readScimBody(w, r)
			var refused scimError
			if errors.As(err, &refused) {
				_ = refused.write(w)
				return
			}
			if err != nil {
				s.scimServerError(w, r, err)
				return
			}
			in.body = b
		}
		forward := r.Clone(context.WithValue(r.Context(), scimInputKey{}, in))
		forward.URL.RawQuery = ""
		forward.Body, forward.ContentLength = http.NoBody, 0
		for _, name := range []string{"If-Match", "X-SCIM-Meta-Version"} {
			if values := forward.Header.Values(name); len(values) > 1 {
				forward.Header.Set(name, strings.Join(values, ","))
			}
		}
		next.ServeHTTP(w, forward)
	})
}

// scimServerErrorDetail is a SCIM 500's detail, the same for every failure.
const scimServerErrorDetail = "An unexpected error occurred."

// scimServerError answers a failure of the ingress itself with a SCIM 500
// and logs it. The answer never echoes the error, and neither the answer
// nor the log names the token or the body.
func (s *server) scimServerError(w http.ResponseWriter, r *http.Request, err error) {
	s.deps.Logger.ErrorContext(r.Context(), "identity: SCIM ingress failed", "path", r.URL.Path, "error", err.Error())
	writeScimError(w, http.StatusInternalServerError, "", scimServerErrorDetail)
}

// utf8BOM is the byte order mark JsonDocument skips at the start of a
// stream.
var utf8BOM = []byte("\xef\xbb\xbf")

// readScimBody is .NET's Body helper (EA/SCIM:36-66): the media type must
// be application/scim+json (parameters and case aside), the body at most
// scimMaxBodyBytes, announced or read, and one JSON document.
func readScimBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	media, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	if !strings.EqualFold(dotnetTrim(media), scimMediaType) {
		return nil, scimUnsupportedMediaType
	}
	if r.ContentLength > scimMaxBodyBytes {
		return nil, scimTooLarge
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, scimMaxBodyBytes))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return nil, scimTooLarge
	}
	if err != nil {
		return nil, fmt.Errorf("identity: read SCIM body: %w", err)
	}
	body = bytes.TrimPrefix(body, utf8BOM)
	if !validScimJSON(body) {
		return nil, scimInvalidJSON
	}
	return body, nil
}

// scimTx runs fn in one READ COMMITTED transaction that first takes the
// SCIM lock, at now, retried while it is chosen as a deadlock victim. .NET
// ran a create SERIALIZABLE (SV/SCIM:123-124) and relied on the mapping's
// ETag as a concurrency token elsewhere; under one lock every SCIM write
// reads what it checks after the write before it committed, so a check
// never interleaves with another's write, and two writes with one ETag
// succeed once.
//
// now is the clock at microseconds, the precision the columns keep, so the
// times a response and an audit row show are the ones stored.
func (s *server) scimTx(ctx context.Context, fn func(q *store.Queries, now time.Time) error) error {
	return db.RetrySerializable(ctx, serializableAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
			q := store.New(tx)
			if err := q.AcquireScimLock(ctx, scimLockKey); err != nil {
				return err
			}
			return fn(q, s.deps.Clock().UTC().Truncate(time.Microsecond))
		})
	})
}

// scimCreatedBefore is a created event's before, .NET's
// {ScimConnectionId, ResourceId} (SV/SCIM:977, :984).
type scimCreatedBefore struct {
	ScimConnectionID uuid.UUID `json:"ScimConnectionId"`
	ResourceID       string    `json:"ResourceId"`
}

// writeScimAudit records a SCIM event on q, the caller's transaction, as
// .NET's WriteAuditAsync did (SV/SCIM:973-985): no actor, the user as the
// target of a User event, and no second factor, since the request carries
// no session.
func writeScimAudit(ctx context.Context, q *store.Queries, r *http.Request, now time.Time, action string, target *uuid.UUID, before, after any) error {
	return writeAudit(ctx, q, r, now, auditEvent{targetUser: target, action: action, before: before, after: after})
}

// newScimETag is a fresh ETag: 32 random hex digits, .NET's
// Guid.NewGuid().ToString("N") (SV/SCIM:960, :968).
func newScimETag() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

// quotedETag is an ETag header's value: the tag in quotes (SV/SCIM:1192).
func quotedETag(tag string) *string {
	v := `"` + tag + `"`
	return &v
}

func (s *server) scimUserLocation(id uuid.UUID) string {
	return s.deps.Config.BasePath + scimBasePath + "/Users/" + id.String()
}

func (s *server) scimGroupLocation(id uuid.UUID) string {
	return s.deps.Config.BasePath + scimBasePath + "/Groups/" + id.String()
}

// GetIdentityScimV2ServiceProviderConfig is ServiceProviderConfigAsync
// (SV/SCIM:51-79): PATCH, filtering (100 results) and ETags are supported;
// bulk, sorting and password changes are not.
func (s *server) GetIdentityScimV2ServiceProviderConfig(context.Context, gen.GetIdentityScimV2ServiceProviderConfigRequestObject) (gen.GetIdentityScimV2ServiceProviderConfigResponseObject, error) {
	var c gen.ScimServiceProviderConfig
	c.Schemas = []string{"urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"}
	c.DocumentationUri = "https://www.rfc-editor.org/rfc/rfc7644"
	c.Patch.Supported = true
	c.Bulk.Supported, c.Bulk.MaxOperations, c.Bulk.MaxPayloadSize = false, 0, 0
	c.Filter.Supported, c.Filter.MaxResults = true, scimMaxPageSize
	c.ChangePassword.Supported = false
	c.Sort.Supported = false
	c.Etag.Supported = true
	c.AuthenticationSchemes = slices.Grow(c.AuthenticationSchemes, 1)[:1] // one scheme, of gen's anonymous element type
	scheme := &c.AuthenticationSchemes[0]
	scheme.Type = "oauthbearertoken"
	scheme.Name = "SCIM bearer token"
	scheme.Description = "Deployment-bound bearer token issued for one SCIM connection."
	scheme.SpecUri = "https://www.rfc-editor.org/rfc/rfc6750"
	scheme.DocumentationUri = "https://www.rfc-editor.org/rfc/rfc7644"
	scheme.Primary = true
	return gen.GetIdentityScimV2ServiceProviderConfig200ApplicationScimPlusJSONResponse(c), nil
}

// scimAttribute is one attribute of a schema document, read-write and
// returned by default as all but one of .NET's were (SV/SCIM:1010-1038).
func scimAttribute(name, typ string, multiValued, required bool) gen.ScimSchemaAttribute {
	return gen.ScimSchemaAttribute{Name: name, Type: typ, MultiValued: multiValued, Required: required, Mutability: "readWrite", Returned: "default"}
}

// caseExact marks a case-exact attribute, and unique one the server keeps
// unique.
func caseExact(a gen.ScimSchemaAttribute) gen.ScimSchemaAttribute {
	exact := true
	a.CaseExact = &exact
	return a
}

func unique(a gen.ScimSchemaAttribute) gen.ScimSchemaAttribute {
	server := "server"
	a.Uniqueness = &server
	return a
}

func withSubAttributes(a gen.ScimSchemaAttribute, sub ...gen.ScimSchemaAttribute) gen.ScimSchemaAttribute {
	a.SubAttributes = &sub
	return a
}

// GetIdentityScimV2Schemas is SchemasAsync (SV/SCIM:81-92) with .NET's two
// schema documents (:1010-1038).
func (s *server) GetIdentityScimV2Schemas(context.Context, gen.GetIdentityScimV2SchemasRequestObject) (gen.GetIdentityScimV2SchemasResponseObject, error) {
	ref := scimAttribute("$ref", "reference", false, false)
	ref.Mutability = "readOnly"
	user := gen.ScimSchemaDocument{
		Id:          scimUserSchema,
		Name:        "User",
		Description: "SCIM User",
		Attributes: []gen.ScimSchemaAttribute{
			unique(caseExact(scimAttribute("userName", "string", false, true))),
			unique(caseExact(scimAttribute("externalId", "string", false, true))),
			scimAttribute("active", "boolean", false, false),
			caseExact(scimAttribute("displayName", "string", false, false)),
			withSubAttributes(scimAttribute("name", "complex", false, false),
				scimAttribute("givenName", "string", false, false),
				scimAttribute("familyName", "string", false, false)),
			withSubAttributes(scimAttribute("emails", "complex", true, false),
				scimAttribute("value", "string", false, false),
				scimAttribute("type", "string", false, false),
				scimAttribute("primary", "boolean", false, false)),
		},
	}
	group := gen.ScimSchemaDocument{
		Id:          scimGroupSchema,
		Name:        "Group",
		Description: "SCIM Group",
		Attributes: []gen.ScimSchemaAttribute{
			unique(caseExact(scimAttribute("externalId", "string", false, false))),
			unique(caseExact(scimAttribute("displayName", "string", false, true))),
			scimAttribute("active", "boolean", false, false),
			withSubAttributes(scimAttribute("members", "complex", true, false),
				scimAttribute("value", "string", false, true),
				ref,
				scimAttribute("type", "string", false, false)),
		},
	}
	return gen.GetIdentityScimV2Schemas200ApplicationScimPlusJSONResponse{
		Schemas:      []string{scimListSchema},
		TotalResults: 2,
		Resources:    []gen.ScimSchemaDocument{user, group},
	}, nil
}

// GetIdentityScimV2ResourceTypes is ResourceTypesAsync (SV/SCIM:94-109).
// The endpoints carry the installation's base path, as every location here
// does.
func (s *server) GetIdentityScimV2ResourceTypes(context.Context, gen.GetIdentityScimV2ResourceTypesRequestObject) (gen.GetIdentityScimV2ResourceTypesResponseObject, error) {
	base := s.deps.Config.BasePath + scimBasePath
	return gen.GetIdentityScimV2ResourceTypes200ApplicationScimPlusJSONResponse{
		Schemas:      []string{"urn:ietf:params:scim:schemas:core:2.0:ResourceType"},
		TotalResults: 2,
		Resources: []gen.ScimResourceType{
			{Id: "User", Name: "User", Endpoint: base + "/Users", Description: "SCIM User", Schema: scimUserSchema},
			{Id: "Group", Name: "Group", Endpoint: base + "/Groups", Description: "SCIM Group", Schema: scimGroupSchema},
		},
	}, nil
}

// The SCIM errors each operation answers with.

func (e scimError) VisitGetIdentityScimV2UsersResponse(w http.ResponseWriter) error {
	return e.write(w)
}

func (e scimError) VisitPostIdentityScimV2UsersResponse(w http.ResponseWriter) error {
	return e.write(w)
}

func (e scimError) VisitGetIdentityScimV2UsersByIdResponse(w http.ResponseWriter) error {
	return e.write(w)
}

func (e scimError) VisitPutIdentityScimV2UsersByIdResponse(w http.ResponseWriter) error {
	return e.write(w)
}

func (e scimError) VisitPatchIdentityScimV2UsersByIdResponse(w http.ResponseWriter) error {
	return e.write(w)
}

func (e scimError) VisitDeleteIdentityScimV2UsersByIdResponse(w http.ResponseWriter) error {
	return e.write(w)
}

func (e scimError) VisitGetIdentityScimV2GroupsResponse(w http.ResponseWriter) error {
	return e.write(w)
}

func (e scimError) VisitPostIdentityScimV2GroupsResponse(w http.ResponseWriter) error {
	return e.write(w)
}

func (e scimError) VisitGetIdentityScimV2GroupsByIdResponse(w http.ResponseWriter) error {
	return e.write(w)
}

func (e scimError) VisitPatchIdentityScimV2GroupsByIdResponse(w http.ResponseWriter) error {
	return e.write(w)
}

func (e scimError) VisitDeleteIdentityScimV2GroupsByIdResponse(w http.ResponseWriter) error {
	return e.write(w)
}
