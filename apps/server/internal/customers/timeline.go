package customers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	apicommon "github.com/vantigo-io/vantigo/server/internal/apicommon/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is the customer timeline's read endpoints, manual entries and
// revision/concurrency machinery (TimelineEndpoints.cs, customers inventory
// §2.3-2.4, §4): getCustomersByIdTimeline, postCustomersByIdTimeline,
// getCustomersByIdTimelineByEntryId, putCustomersByIdTimelineByEntryId,
// deleteCustomersByIdTimelineByEntryId and
// getCustomersByIdTimelineByEntryIdRevisions.
//
// The three "generated" events a customer's own create/update/archive and
// its contact associations emit (customer.created/updated/status_changed,
// customer.contact_*) are Task 6 and Task 7's timeline_events.go, already
// wired into the customer and contact handlers — nothing here writes a
// generated event a second time. This file only adds manual entries (always
// Provenance=manual, ActorKind=unattributed) and the endpoints that read
// both kinds back.
//
// customers inventory §1.1 lists no permission on any of these six that is
// conditional on the request body the way postCustomers/putCustomersById's
// legal-identity-manage is: every x-vantigo-access here is a flat permission
// or `+`-joined pair module.Router enforces in full, so none of these
// handlers make a second Access.Check the way PostCustomers/PutCustomersById
// do.

// manualTimelineEventTypes is ManualTypes (TimelineEndpoints.cs:21-29): the
// only eventType values a manual entry may carry. Generated event types like
// customer.created can never be produced through this endpoint.
var manualTimelineEventTypes = []string{
	"registry.change",
	"interaction.call",
	"interaction.meeting",
	"interaction.email",
	"note",
	"other",
}

const civilDateLayout = "2006-01-02"

func manualEventTypeAllowed(v string) bool {
	for _, t := range manualTimelineEventTypes {
		if v == t {
			return true
		}
	}
	return false
}

// civilDate truncates t to its UTC calendar date at midnight, the Go
// equivalent of .NET's DateOnly.
func civilDate(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// parseISODate is DateOnly.TryParseExact(value, "yyyy-MM-dd", ...): a strict
// four-digit-year ISO date, not the looser forms time.Parse alone would
// otherwise accept.
func parseISODate(s string) (time.Time, error) {
	if len(s) != len(civilDateLayout) {
		return time.Time{}, fmt.Errorf("customers: %q is not an ISO date", s)
	}
	return time.Parse(civilDateLayout, s)
}

// truncateUTF16 is .NET's Note[..Math.Min(Note.Length, max)]: a substring
// taken in UTF-16 code units, not Go's byte or rune count, so the 500-char
// cap on a manual entry's derived summary matches .NET's for the same input.
func truncateUTF16(s string, max int) string {
	units := utf16.Encode([]rune(s))
	if len(units) <= max {
		return s
	}
	return string(utf16.Decode(units[:max]))
}

// parsedManualTimeline is ParsedManualTimeline (TimelineEndpoints.cs:546-551):
// the validated, normalized values of a manual timeline request.
type parsedManualTimeline struct {
	EventType  string
	OccurredOn time.Time
	OccurredAt *time.Time
	Note       string
	SourceURL  *string
}

// validateManualTimelineRequest is TryParseManual (TimelineEndpoints.cs:366-430,
// customers inventory §2.3): every field is validated independently and every
// error collected together, unlike Update/Attach's early-return style
// elsewhere in this module.
func validateManualTimelineRequest(body gen.TimelineManualTimelineRequest, now time.Time) (parsedManualTimeline, map[string][]string) {
	errs := map[string][]string{}

	eventType := deref(body.EventType)
	if strings.TrimSpace(eventType) == "" || !manualEventTypeAllowed(eventType) {
		errs["eventType"] = []string{fmt.Sprintf("EventType must be one of: %s", strings.Join(manualTimelineEventTypes, ", "))}
	}

	occurredOnStr := deref(body.OccurredOn)
	occurredOn, onErr := parseISODate(occurredOnStr)
	if onErr != nil {
		errs["occurredOn"] = []string{"OccurredOn must be an ISO date (yyyy-MM-dd)"}
	} else if occurredOn.After(civilDate(now)) {
		errs["occurredOn"] = []string{"An occurrence date cannot be in the future"}
	}

	var note string
	switch {
	case body.Note == nil || strings.TrimSpace(*body.Note) == "":
		errs["note"] = []string{"A nonblank note or description is required"}
	case utf16Length(*body.Note) > 10000:
		errs["note"] = []string{"A note cannot be longer than 10000 characters"}
	default:
		note = strings.TrimSpace(*body.Note)
	}

	var occurredAt *time.Time
	if body.OccurredAt != nil {
		at := body.OccurredAt.UTC()
		switch {
		case at.After(now.UTC()):
			errs["occurredAt"] = []string{"An occurrence instant cannot be in the future"}
		case !civilDate(at).Equal(occurredOn):
			errs["occurredAt"] = []string{"OccurredAt must have the same UTC calendar date as occurredOn"}
		default:
			occurredAt = &at
		}
	}

	var sourceURL *string
	if body.SourceUrl != nil {
		u, err := url.Parse(*body.SourceUrl)
		switch {
		// .NET's Uri.TryCreate(..., Absolute, ...) requires a host for an
		// http(s) URI; Go's url.Parse happily accepts "https://" or
		// "https:///path" with Host == "", wider than .NET's shape, so the
		// host check is required alongside IsAbs()/scheme, not implied by
		// them.
		case err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
			errs["sourceUrl"] = []string{"SourceUrl must be an absolute http(s) URL"}
		case utf16Length(*body.SourceUrl) > 2048:
			errs["sourceUrl"] = []string{"SourceUrl cannot be longer than 2048 characters"}
		default:
			sourceURL = body.SourceUrl
		}
	}

	if len(errs) > 0 {
		return parsedManualTimeline{}, errs
	}
	return parsedManualTimeline{EventType: eventType, OccurredOn: occurredOn, OccurredAt: occurredAt, Note: note, SourceURL: sourceURL}, nil
}

// timelineFilters is TimelineFilters (TimelineEndpoints.cs:560-578): the
// normalized, validated query filters a timeline listing applies, and the
// same values a cursor is scoped to (cursorMatches below).
type timelineFilters struct {
	Provenance   *string
	EventTypes   []string
	OccurredFrom *time.Time
	OccurredTo   *time.Time
}

// parseTimelineFilters is TryParseFilters (TimelineEndpoints.cs:473-516):
// every check runs regardless of the others, and every failure is collected
// together into one 400.
func parseTimelineFilters(params gen.GetCustomersByIdTimelineParams) (timelineFilters, map[string][]string) {
	errs := map[string][]string{}

	var provenance *string
	if params.Provenance != nil {
		switch *params.Provenance {
		case "manual", "generated":
			p := *params.Provenance
			provenance = &p
		default:
			errs["provenance"] = []string{"Provenance must be either 'manual' or 'generated'"}
		}
	}

	var eventTypes []string
	if params.EventType != nil {
		for i, et := range *params.EventType {
			switch {
			case strings.TrimSpace(et) == "":
				errs[fmt.Sprintf("eventType[%d]", i)] = []string{"EventType cannot be blank"}
			case utf16Length(et) > 100:
				errs[fmt.Sprintf("eventType[%d]", i)] = []string{"EventType cannot be longer than 100 characters"}
			default:
				eventTypes = append(eventTypes, et)
			}
		}
	}

	from := parseFilterDate(params.OccurredFrom, "occurredFrom", errs)
	to := parseFilterDate(params.OccurredTo, "occurredTo", errs)
	if from != nil && to != nil && from.After(*to) {
		errs["occurredFrom"] = []string{"OccurredFrom cannot be later than occurredTo"}
	}

	if len(errs) > 0 {
		return timelineFilters{}, errs
	}
	return timelineFilters{Provenance: provenance, EventTypes: normalizeEventTypes(eventTypes), OccurredFrom: from, OccurredTo: to}, nil
}

// parseFilterDate is ParseFilterDate (TimelineEndpoints.cs:518-533).
func parseFilterDate(value *string, key string, errs map[string][]string) *time.Time {
	if value == nil {
		return nil
	}
	t, err := parseISODate(*value)
	if err != nil {
		errs[key] = []string{fmt.Sprintf("%s must be an ISO date (yyyy-MM-dd)", key)}
		return nil
	}
	return &t
}

// normalizeEventTypes sorts and dedupes ordinally, as GetDigest's
// Order(StringComparer.Ordinal).Distinct() does for the cursor comparison;
// applying it to the query filter too changes nothing a set membership test
// (`= ANY(...)`) observes. Never nil, so the query's "no filter" branch
// (`@event_types::text[] IS NULL`) is never accidentally taken for an empty,
// but present, filter list.
func normalizeEventTypes(vals []string) []string {
	if len(vals) == 0 {
		return []string{}
	}
	sorted := append([]string(nil), vals...)
	sort.Strings(sorted)
	out := sorted[:1]
	for _, v := range sorted[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

// timelineCursorPayload is this port's opaque keyset cursor: the last row of
// a page plus the exact filters the caller listed under, so a cursor reused
// against a different customer or different filters is rejected
// (TimelineCursor/TimelineFilters.GetDigest, TimelineEndpoints.cs:440-471,
// 553-578). It need not match .NET's own cursor bytes — the cursor is opaque
// to callers on both sides of the port — only round-trip consistently within
// this implementation.
type timelineCursorPayload struct {
	CustomerID   int32    `json:"customerId"`
	OccurredOn   string   `json:"occurredOn"`
	OccurredAt   *string  `json:"occurredAt,omitempty"`
	ID           int32    `json:"id"`
	Provenance   *string  `json:"provenance,omitempty"`
	EventTypes   []string `json:"eventTypes,omitempty"`
	OccurredFrom *string  `json:"occurredFrom,omitempty"`
	OccurredTo   *string  `json:"occurredTo,omitempty"`

	// occurredAtTime is OccurredAt parsed exactly once, by
	// decodeTimelineCursor, and never re-parsed afterward: re-parsing later
	// (in the List handler, after cursorMatches had already accepted the
	// cursor) is what let an unparsable occurredAt reach time.Parse's error
	// as a 500 instead of decodeTimelineCursor's own 400 — customers
	// inventory §1.4 requires every malformed cursor, occurredAt included,
	// to answer 400. Unexported: it is decode-time-only derived data, never
	// part of the wire payload.
	occurredAtTime *time.Time
}

func formatFilterDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(civilDateLayout)
	return &s
}

// encodeTimelineCursor is EncodeCursor (TimelineEndpoints.cs:440-446).
func encodeTimelineCursor(e store.CustomersCustomersTimelineEntry, customerID int32, f timelineFilters) string {
	payload := timelineCursorPayload{
		CustomerID:   customerID,
		OccurredOn:   e.OccurredOn.Time.Format(civilDateLayout),
		ID:           e.ID,
		Provenance:   f.Provenance,
		EventTypes:   f.EventTypes,
		OccurredFrom: formatFilterDate(f.OccurredFrom),
		OccurredTo:   formatFilterDate(f.OccurredTo),
	}
	if e.OccurredAt != nil {
		s := e.OccurredAt.UTC().Format(time.RFC3339Nano)
		payload.OccurredAt = &s
	}
	b, err := json.Marshal(payload)
	if err != nil {
		// payload is built entirely from values this process already
		// validated and persisted; it cannot fail to marshal.
		panic(fmt.Sprintf("customers: encode timeline cursor: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeTimelineCursor is TryDecodeCursor (TimelineEndpoints.cs:448-471): any
// malformed input, base64 or JSON, answers ok=false, the caller's "cursor is
// malformed" 400.
func decodeTimelineCursor(value string) (timelineCursorPayload, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return timelineCursorPayload{}, false
	}
	var payload timelineCursorPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return timelineCursorPayload{}, false
	}
	if payload.ID <= 0 || payload.OccurredOn == "" {
		return timelineCursorPayload{}, false
	}
	if _, err := parseISODate(payload.OccurredOn); err != nil {
		return timelineCursorPayload{}, false
	}
	if payload.OccurredAt != nil {
		at, err := time.Parse(time.RFC3339Nano, *payload.OccurredAt)
		if err != nil {
			return timelineCursorPayload{}, false
		}
		payload.occurredAtTime = &at
	}
	return payload, true
}

func stringPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// cursorMatches is the cursor-vs-filters comparison List makes before
// applying a cursor (TimelineEndpoints.cs:68-74): a cursor scoped to another
// customer or a different set of filters is rejected, never silently
// reinterpreted. It deliberately does not compare OccurredOn/OccurredAt/ID:
// those are the cursor's pagination position, not a filter, and .NET's own
// TimelineFilters.GetDigest — the value this comparison stands in for —
// never hashes them either (TimelineEndpoints.cs:566-577 hashes only
// customerId/provenance/eventTypes/occurredFrom/occurredTo). Comparing the
// position fields here would reject every second page of a multi-page
// listing, since each page's cursor legitimately carries a different
// position while scoped to the same customer and filters.
func cursorMatches(c timelineCursorPayload, customerID int32, f timelineFilters) bool {
	return c.CustomerID == customerID &&
		stringPtrEqual(c.Provenance, f.Provenance) &&
		stringSliceEqual(c.EventTypes, f.EventTypes) &&
		stringPtrEqual(c.OccurredFrom, formatFilterDate(f.OccurredFrom)) &&
		stringPtrEqual(c.OccurredTo, formatFilterDate(f.OccurredTo))
}

func dateParam(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: *t, Valid: true}
}

// fromInsertManualRow adapts InsertManualTimelineEntry's CTE-shaped result
// (sqlc generates a distinct Row type for it, InsertManualTimelineEntryRow,
// even though its columns are identical to customers_timeline_entries) into
// the same store.CustomersCustomersTimelineEntry every other timeline query
// returns, so timelineResponse has only one shape to know.
func fromInsertManualRow(r store.InsertManualTimelineEntryRow) store.CustomersCustomersTimelineEntry {
	return store.CustomersCustomersTimelineEntry(r)
}

// payloadElement decodes a stored payload_json column into the contract's
// JsonElement (an untyped interface{}, apicommon's JsonElement alias), nil
// for a manual entry, which never carries one.
func payloadElement(raw []byte) *apicommon.JsonElement {
	if len(raw) == 0 {
		return nil
	}
	var v apicommon.JsonElement
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return &v
}

// timelineResponse is TimelineResponse.FromDomain (TimelineEndpoints.cs:612-632).
func timelineResponse(e store.CustomersCustomersTimelineEntry) gen.TimelineResponse {
	return gen.TimelineResponse{
		Id:              e.ID,
		EventType:       e.EventType,
		Provenance:      e.Provenance,
		Producer:        e.Producer,
		OccurredOn:      openapi_types.Date{Time: e.OccurredOn.Time},
		OccurredAt:      e.OccurredAt,
		Summary:         ptr(e.Summary),
		Note:            e.Note,
		SourceUrl:       e.SourceUrl,
		Payload:         payloadElement(e.PayloadJson),
		CurrentRevision: e.CurrentRevision,
		State:           e.State,
		ActorKind:       e.ActorKind,
		ActorDisplay:    ptr(e.ActorDisplay),
		CreatedAt:       e.CreatedAt,
		UpdatedAt:       e.UpdatedAt,
	}
}

// timelineRevisionResponse is TimelineRevisionResponse.FromDomain
// (TimelineEndpoints.cs:655-677): Action is derived purely from
// State==Deleted / RevisionNumber==1 / else "update" — it is never stored,
// only computed at response time (inventory oddity #6).
func timelineRevisionResponse(r store.CustomersCustomersTimelineEntriesRevision) gen.TimelineRevisionResponse {
	action := "update"
	switch {
	case r.State == "deleted":
		action = "delete"
	case r.RevisionNumber == 1:
		action = "create"
	}
	actorDisplayName := r.ActorDisplay
	if strings.TrimSpace(actorDisplayName) == "" {
		actorDisplayName = "Unattributed"
	}
	return gen.TimelineRevisionResponse{
		Revision:         r.RevisionNumber,
		EventType:        r.EventType,
		Action:           action,
		ChangedAt:        r.CreatedAt,
		Provenance:       r.Provenance,
		Producer:         r.Producer,
		OccurredOn:       openapi_types.Date{Time: r.OccurredOn.Time},
		OccurredAt:       r.OccurredAt,
		Summary:          ptr(r.Summary),
		Note:             r.Note,
		SourceUrl:        r.SourceUrl,
		Payload:          payloadElement(r.PayloadJson),
		State:            r.State,
		ActorKind:        r.ActorKind,
		ActorDisplayName: actorDisplayName,
		DeletedAt:        r.DeletedAt,
	}
}

// timelineProblem is Conflict (TimelineEndpoints.cs:437-438): every
// concurrency refusal in this file, all three guards alike, answers this
// same problem shape — title and detail text are the only way a caller (or
// a test) can tell which guard caught it.
func timelineProblem(title, detail string) apicommon.ProblemDetails {
	return problemStatus(title, detail, http.StatusConflict)
}

const timelineImmutableTitle = "Timeline entry is immutable"
const timelineRevisionConflictTitle = "Timeline revision conflict"

// insertTimelineRevisionFromEntry is AddRevision (TimelineEndpoints.cs:343-364):
// a full point-in-time snapshot of e, taken right after a guarded write
// (UpdateManualTimelineEntry or SetTimelineEntryDeleted) succeeded. Its
// uniqueness on (entry id, revision number) is the third concurrency guard,
// customers inventory §4.
func insertTimelineRevisionFromEntry(ctx context.Context, q *store.Queries, e store.CustomersCustomersTimelineEntry) error {
	return q.InsertTimelineRevision(ctx, store.InsertTimelineRevisionParams{
		EntryID:         e.ID,
		RevisionNumber:  e.CurrentRevision,
		CustomerID:      e.CustomerID,
		Provenance:      e.Provenance,
		Producer:        e.Producer,
		EventType:       e.EventType,
		OccurredOn:      e.OccurredOn,
		OccurredAt:      e.OccurredAt,
		Summary:         e.Summary,
		Note:            e.Note,
		SourceUrl:       e.SourceUrl,
		PayloadJson:     e.PayloadJson,
		PayloadVersion:  e.PayloadVersion,
		CurrentRevision: e.CurrentRevision,
		State:           e.State,
		ActorKind:       e.ActorKind,
		ActorDisplay:    e.ActorDisplay,
		CreatedAt:       e.CreatedAt,
		UpdatedAt:       e.UpdatedAt,
		DeletedAt:       e.DeletedAt,
	})
}

// timelineRevisionUniqueConstraint is the unique index name
// IsRevisionConflict matches on (TimelineEndpoints.cs:432-435): the third,
// independent concurrency guard, a Postgres 23505 on exactly this
// constraint.
const timelineRevisionUniqueConstraint = "ux_customers_timeline_entries_revisions_entry_revision"

// GetCustomersByIdTimeline List a customer's timeline
// (GET /api/v1/customers/{id}/timeline)
//
// Ordering follows TimelineEndpoints.List (customers inventory §1.4): (1)
// limit range; (2) filter shape; (3) customer existence — a malformed
// cursor against a nonexistent customer answers 404, not 400; (4) cursor
// decode; (5) cursor-matches-filters.
func (s *server) GetCustomersByIdTimeline(ctx context.Context, req gen.GetCustomersByIdTimelineRequestObject) (gen.GetCustomersByIdTimelineResponseObject, error) {
	const defaultLimit = 25
	const maxLimit = 100

	if req.Params.Limit != nil && (*req.Params.Limit <= 0 || *req.Params.Limit > maxLimit) {
		return gen.GetCustomersByIdTimeline400ApplicationProblemPlusJSONResponse(validationProblem("Invalid timeline query", map[string][]string{
			"limit": {fmt.Sprintf("Limit must be between 1 and %d", maxLimit)},
		})), nil
	}

	filters, errs := parseTimelineFilters(req.Params)
	if errs != nil {
		return gen.GetCustomersByIdTimeline400ApplicationProblemPlusJSONResponse(validationProblem("Invalid timeline query", errs)), nil
	}

	q := store.New(s.deps.Pool)
	if _, err := q.GetCustomer(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomersByIdTimeline404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}

	var cursor timelineCursorPayload
	var hasCursor bool
	if req.Params.Cursor != nil {
		decoded, ok := decodeTimelineCursor(*req.Params.Cursor)
		if !ok {
			return gen.GetCustomersByIdTimeline400ApplicationProblemPlusJSONResponse(validationProblem("Invalid timeline query", map[string][]string{
				"cursor": {"The cursor is malformed"},
			})), nil
		}
		if !cursorMatches(decoded, req.Id, filters) {
			return gen.GetCustomersByIdTimeline400ApplicationProblemPlusJSONResponse(validationProblem("Invalid timeline query", map[string][]string{
				"cursor": {"The cursor does not match the requested timeline filters"},
			})), nil
		}
		cursor, hasCursor = decoded, true
	}

	limit := int32(defaultLimit)
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}

	params := store.ListTimelineEntriesParams{
		CustomerID:   req.Id,
		Provenance:   filters.Provenance,
		EventTypes:   filters.EventTypes,
		OccurredFrom: dateParam(filters.OccurredFrom),
		OccurredTo:   dateParam(filters.OccurredTo),
		Take:         limit + 1,
		HasCursor:    hasCursor,
	}
	if hasCursor {
		cursorOn, err := parseISODate(cursor.OccurredOn)
		if err != nil {
			return nil, fmt.Errorf("customers: re-parse cursor date: %w", err)
		}
		params.CursorOccurredOn = pgtype.Date{Time: cursorOn, Valid: true}
		params.CursorID = cursor.ID
		if cursor.occurredAtTime != nil {
			params.CursorHasOccurredAt = true
			params.CursorOccurredAt = *cursor.occurredAtTime
		}
	}

	rows, err := q.ListTimelineEntries(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("customers: list timeline entries: %w", err)
	}

	hasNext := int32(len(rows)) > limit
	if hasNext {
		rows = rows[:limit]
	}
	var nextCursor *string
	if hasNext && len(rows) > 0 {
		c := encodeTimelineCursor(rows[len(rows)-1], req.Id, filters)
		nextCursor = &c
	}

	data := make([]gen.TimelineResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, timelineResponse(r))
	}
	return gen.GetCustomersByIdTimeline200JSONResponse{Data: data, NextCursor: nextCursor}, nil
}

// createdTimelineResponse is PostCustomersByIdTimeline's 201. The generated
// PostCustomersByIdTimeline201JSONResponse has no Location header because
// the contract declares none for this response; this type adds it directly,
// as .NET's TypedResults.Created does (TimelineEndpoints.cs:166) — the same
// pattern contacts.go's createdContactResponse follows for the same reason.
type createdTimelineResponse struct {
	body     gen.TimelineResponse
	location string
}

func (r createdTimelineResponse) VisitPostCustomersByIdTimelineResponse(w http.ResponseWriter) error {
	w.Header().Set("Location", r.location)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	return json.NewEncoder(w).Encode(r.body)
}

// PostCustomersByIdTimeline Create a manual customer timeline entry
// (POST /api/v1/customers/{id}/timeline)
//
// Ordering follows TimelineEndpoints.Create (customers inventory §1.4): (1)
// manual-entry validation, every field collected together, before any
// database access; (2) customer existence.
func (s *server) PostCustomersByIdTimeline(ctx context.Context, req gen.PostCustomersByIdTimelineRequestObject) (gen.PostCustomersByIdTimelineResponseObject, error) {
	body := gen.TimelineManualTimelineRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	now := s.deps.Clock()
	parsed, errs := validateManualTimelineRequest(body, now)
	if errs != nil {
		return gen.PostCustomersByIdTimeline400ApplicationProblemPlusJSONResponse(validationProblem("Invalid timeline entry", errs)), nil
	}

	q := store.New(s.deps.Pool)
	if _, err := q.GetCustomer(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCustomersByIdTimeline404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}

	created, err := q.InsertManualTimelineEntry(ctx, store.InsertManualTimelineEntryParams{
		CustomerID: req.Id,
		EventType:  parsed.EventType,
		OccurredOn: pgtype.Date{Time: parsed.OccurredOn, Valid: true},
		OccurredAt: parsed.OccurredAt,
		Summary:    truncateUTF16(parsed.Note, 500),
		Note:       parsed.Note,
		SourceUrl:  parsed.SourceURL,
		Now:        now,
	})
	if err != nil {
		return nil, fmt.Errorf("customers: create timeline entry: %w", err)
	}

	return createdTimelineResponse{
		body:     timelineResponse(fromInsertManualRow(created)),
		location: fmt.Sprintf("%s/api/v1/customers/%d/timeline/%d", s.deps.Config.BasePath, req.Id, created.ID),
	}, nil
}

// GetCustomersByIdTimelineByEntryId Get a customer timeline entry
// (GET /api/v1/customers/{id}/timeline/{entryId})
//
// Only active entries are visible here (TimelineEndpoints.Get,
// TimelineEndpoints.cs:175-178); a soft-deleted entry answers 404, same as a
// never-existed one.
func (s *server) GetCustomersByIdTimelineByEntryId(ctx context.Context, req gen.GetCustomersByIdTimelineByEntryIdRequestObject) (gen.GetCustomersByIdTimelineByEntryIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	entry, err := q.GetActiveTimelineEntry(ctx, store.GetActiveTimelineEntryParams{ID: req.EntryId, CustomerID: req.Id})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomersByIdTimelineByEntryId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get timeline entry: %w", err)
	}
	return gen.GetCustomersByIdTimelineByEntryId200JSONResponse(timelineResponse(entry)), nil
}

// PutCustomersByIdTimelineByEntryId Update a manual customer timeline entry
// (PUT /api/v1/customers/{id}/timeline/{entryId})
//
// The three overlapping concurrency guards customers inventory §4 describes,
// in the order TimelineEndpoints.Update applies them:
//  1. a manual comparison against the just-loaded row's CurrentRevision, in
//     Go, before any write is attempted (:204-213) — the same "if" also
//     answers "Timeline entry is immutable" for a non-manual or non-active
//     entry, regardless of the supplied expectedRevision;
//  2. current_revision repeated as the guarded UPDATE's WHERE clause, .NET's
//     concurrency-token equivalent (queries/timeline.sql's
//     UpdateManualTimelineEntry) — zero rows affected (pgx.ErrNoRows) means a
//     concurrent writer that read the identical row won the race;
//  3. the new revision row's insert, protected by the unique index on
//     (entry id, revision number) — a 23505 on that exact constraint is the
//     belt-and-suspenders backstop, never relied on for correctness given (2).
//
// All three answer the same "Timeline revision conflict" problem shape;
// their detail text is the only thing that tells them apart (byte-exact from
// TimelineEndpoints.cs:207-230).
func (s *server) PutCustomersByIdTimelineByEntryId(ctx context.Context, req gen.PutCustomersByIdTimelineByEntryIdRequestObject) (gen.PutCustomersByIdTimelineByEntryIdResponseObject, error) {
	body := gen.TimelineManualTimelineRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	now := s.deps.Clock()
	parsed, errs := validateManualTimelineRequest(body, now)
	if errs != nil {
		return gen.PutCustomersByIdTimelineByEntryId400ApplicationProblemPlusJSONResponse(validationProblem("Invalid timeline entry", errs)), nil
	}
	var expectedRevision int32
	if body.ExpectedRevision != nil {
		expectedRevision = *body.ExpectedRevision
	}

	q := store.New(s.deps.Pool)
	entry, err := q.GetTimelineEntry(ctx, store.GetTimelineEntryParams{ID: req.EntryId, CustomerID: req.Id})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdTimelineByEntryId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get timeline entry: %w", err)
	}

	if entry.Provenance != "manual" || entry.State != "active" {
		return gen.PutCustomersByIdTimelineByEntryId409ApplicationProblemPlusJSONResponse(timelineProblem(
			timelineImmutableTitle, "Generated, deleted, or voided timeline entries cannot be edited.")), nil
	}
	if expectedRevision != entry.CurrentRevision {
		return gen.PutCustomersByIdTimelineByEntryId409ApplicationProblemPlusJSONResponse(timelineProblem(
			timelineRevisionConflictTitle,
			fmt.Sprintf("The timeline entry has revision %d; the supplied expectedRevision was %d.", entry.CurrentRevision, expectedRevision))), nil
	}

	newRevision := entry.CurrentRevision + 1
	var updated store.CustomersCustomersTimelineEntry
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		updated, err = txq.UpdateManualTimelineEntry(ctx, store.UpdateManualTimelineEntryParams{
			ID:               req.EntryId,
			CustomerID:       req.Id,
			EventType:        parsed.EventType,
			OccurredOn:       pgtype.Date{Time: parsed.OccurredOn, Valid: true},
			OccurredAt:       parsed.OccurredAt,
			Note:             parsed.Note,
			Summary:          truncateUTF16(parsed.Note, 500),
			SourceUrl:        parsed.SourceURL,
			NewRevision:      newRevision,
			Now:              now,
			ExpectedRevision: expectedRevision,
		})
		if err != nil {
			return err
		}
		return insertTimelineRevisionFromEntry(ctx, txq, updated)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.PutCustomersByIdTimelineByEntryId409ApplicationProblemPlusJSONResponse(timelineProblem(
			timelineRevisionConflictTitle, "The timeline entry was changed by another request.")), nil
	case db.IsUniqueViolation(err, timelineRevisionUniqueConstraint):
		return gen.PutCustomersByIdTimelineByEntryId409ApplicationProblemPlusJSONResponse(timelineProblem(
			timelineRevisionConflictTitle, "The timeline entry revision was concurrently changed.")), nil
	case err != nil:
		return nil, fmt.Errorf("customers: update timeline entry: %w", err)
	}

	return gen.PutCustomersByIdTimelineByEntryId200JSONResponse(timelineResponse(updated)), nil
}

// DeleteCustomersByIdTimelineByEntryId Delete a manual customer timeline entry
// (DELETE /api/v1/customers/{id}/timeline/{entryId})
//
// Soft delete: State becomes deleted, DeletedAt is stamped, and a final
// "delete" revision is appended — the same three concurrency guards as
// Update above, applied by TimelineEndpoints.Delete (:249-278). A missing
// expectedRevision is treated as a revision mismatch, never as "no check
// requested" (`expectedRevision is null || ... != entry.CurrentRevision`).
func (s *server) DeleteCustomersByIdTimelineByEntryId(ctx context.Context, req gen.DeleteCustomersByIdTimelineByEntryIdRequestObject) (gen.DeleteCustomersByIdTimelineByEntryIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	entry, err := q.GetTimelineEntry(ctx, store.GetTimelineEntryParams{ID: req.EntryId, CustomerID: req.Id})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteCustomersByIdTimelineByEntryId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get timeline entry: %w", err)
	}

	if entry.Provenance != "manual" || entry.State != "active" {
		return gen.DeleteCustomersByIdTimelineByEntryId409ApplicationProblemPlusJSONResponse(timelineProblem(
			timelineImmutableTitle, "Generated, deleted, or voided timeline entries cannot be deleted.")), nil
	}
	expected := req.Params.ExpectedRevision
	if expected == nil || *expected != entry.CurrentRevision {
		supplied := "missing"
		if expected != nil {
			supplied = strconv.FormatInt(int64(*expected), 10)
		}
		return gen.DeleteCustomersByIdTimelineByEntryId409ApplicationProblemPlusJSONResponse(timelineProblem(
			timelineRevisionConflictTitle,
			fmt.Sprintf("The timeline entry has revision %d; the supplied expectedRevision was %s.", entry.CurrentRevision, supplied))), nil
	}

	now := s.deps.Clock()
	newRevision := entry.CurrentRevision + 1
	var deleted store.CustomersCustomersTimelineEntry
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		deleted, err = txq.SetTimelineEntryDeleted(ctx, store.SetTimelineEntryDeletedParams{
			ID:               req.EntryId,
			CustomerID:       req.Id,
			Now:              now,
			NewRevision:      newRevision,
			ExpectedRevision: *expected,
		})
		if err != nil {
			return err
		}
		return insertTimelineRevisionFromEntry(ctx, txq, deleted)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.DeleteCustomersByIdTimelineByEntryId409ApplicationProblemPlusJSONResponse(timelineProblem(
			timelineRevisionConflictTitle, "The timeline entry was changed by another request.")), nil
	case db.IsUniqueViolation(err, timelineRevisionUniqueConstraint):
		return gen.DeleteCustomersByIdTimelineByEntryId409ApplicationProblemPlusJSONResponse(timelineProblem(
			timelineRevisionConflictTitle, "The timeline entry revision was concurrently changed.")), nil
	case err != nil:
		return nil, fmt.Errorf("customers: delete timeline entry: %w", err)
	}
	return gen.DeleteCustomersByIdTimelineByEntryId204Response{}, nil
}

// GetCustomersByIdTimelineByEntryIdRevisions List timeline entry revisions
// (GET /api/v1/customers/{id}/timeline/{entryId}/revisions)
//
// Unlike Get, existence here is any state (TimelineEndpoints.Revisions
// :289-291): a soft-deleted or generated entry's history is still visible.
func (s *server) GetCustomersByIdTimelineByEntryIdRevisions(ctx context.Context, req gen.GetCustomersByIdTimelineByEntryIdRevisionsRequestObject) (gen.GetCustomersByIdTimelineByEntryIdRevisionsResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetTimelineEntry(ctx, store.GetTimelineEntryParams{ID: req.EntryId, CustomerID: req.Id}); errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomersByIdTimelineByEntryIdRevisions404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("customers: get timeline entry: %w", err)
	}

	rows, err := q.ListTimelineRevisions(ctx, req.EntryId)
	if err != nil {
		return nil, fmt.Errorf("customers: list timeline revisions: %w", err)
	}
	data := make([]gen.TimelineRevisionResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, timelineRevisionResponse(r))
	}
	return gen.GetCustomersByIdTimelineByEntryIdRevisions200JSONResponse{Data: data}, nil
}
