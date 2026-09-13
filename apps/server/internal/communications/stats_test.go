package communications_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the Stats area's tests (EP/CommunicationsStatsEndpoints.cs,
// communications inventory §1.6, §2's "Stats summary/timeseries" bullet, §3
// items 1-2, §3.3, §19 items 19 and 21; task 9's dispatch and its
// correction are the authority for the behaviour pinned here) for
// getCommunicationsStatsSummary, getCommunicationsStatsTimeseries and
// getCommunicationsStatsAttention.
//
// No .NET test class exists for CommunicationsStatsEndpoints.cs — a
// repo-wide search of Communications.Module.Tests turns up nothing
// referencing "Stats" — the same gap customers/stats_test.go and
// products/stats_test.go each document for their own modules. Every test
// below is this task's own, written directly against the .NET handler's
// behaviour (and the inventory's own reading of it) rather than ported from
// an existing test.
//
// Fixtures are built with direct SQL inserts (h.Exec) rather than through
// the API wherever a test needs exact control over created_at/occurred_at/
// last_activity_at or over a status (failed/submission_failed deliveries)
// no production code path in this port ever writes yet — the same
// technique conversations_test.go and conversations_reply_test.go already
// use for their own out-of-band fixtures.

// ---- Fixture helpers ----

// insertCommunicationsConversation inserts one conversation row directly,
// for tests that need exact control over created_at/last_activity_at/status
// that the API's own "now" clock cannot give them.
func insertCommunicationsConversation(t *testing.T, h *modtest.Harness, chID string, status string, createdAt, lastActivityAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.Exec(t, `INSERT INTO communications.conversations (id, channel_id, status, last_activity_at, created_at)
	           VALUES ($1, $2, $3, $4, $5)`, id, uuid.MustParse(chID), status, lastActivityAt, createdAt)
	return id
}

// insertCommunicationsMessage inserts one outbound conversation_messages
// row directly, occurring at occurredAt.
func insertCommunicationsMessage(t *testing.T, h *modtest.Harness, convID uuid.UUID, occurredAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.Exec(t, `INSERT INTO communications.conversation_messages (id, conversation_id, direction, occurred_at, created_at)
	           VALUES ($1, $2, 'outbound', $3, $3)`, id, convID, occurredAt)
	return id
}

// insertCommunicationsInboundMessage inserts one direction='inbound'
// conversation_messages row directly, occurring at occurredAt. No
// production code path in this port ever writes 'inbound' (design doc
// §1.1: the inbound worker was the only writer and it is out of scope), but
// a fixture can since task 7 fix round 2 widened
// conversation_messages.direction's CHECK to match .NET's full Direction
// domain — this is what lets TestGetCommunicationsStatsAttention_IncludesOpenConversationsWithAnInboundNewestMessage
// drive the attention query's "conversationNoReply" arm to its live branch.
func insertCommunicationsInboundMessage(t *testing.T, h *modtest.Harness, convID uuid.UUID, occurredAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.Exec(t, `INSERT INTO communications.conversation_messages (id, conversation_id, direction, occurred_at, created_at)
	           VALUES ($1, $2, 'inbound', $3, $3)`, id, convID, occurredAt)
	return id
}

// insertCommunicationsDelivery inserts one message_deliveries row directly
// with an explicit status and created_at — InsertMessageDelivery (the only
// production insert path) always writes 'queued', so a failed/
// submission_failed fixture can only be built this way today.
func insertCommunicationsDelivery(t *testing.T, h *modtest.Harness, messageID uuid.UUID, status string, createdAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.Exec(t, `INSERT INTO communications.message_deliveries (id, message_id, recipient_address, recipient_type, status, attempts, created_at)
	           VALUES ($1, $2, 'recipient@example.test', 'to', $3, 1, $4)`, id, messageID, status, createdAt)
	return id
}

// ---- JSON response shapes ----

type communicationsStatsSummaryJSON struct {
	From                     time.Time `json:"from"`
	To                       time.Time `json:"to"`
	OpenConversations        int       `json:"openConversations"`
	OpenConversationsDelta   int       `json:"openConversationsDelta"`
	NewConversations         int       `json:"newConversations"`
	NewConversationsDelta    int       `json:"newConversationsDelta"`
	Messages                 int       `json:"messages"`
	MessagesDelta            int       `json:"messagesDelta"`
	ClosedConversations      int       `json:"closedConversations"`
	ClosedConversationsDelta int       `json:"closedConversationsDelta"`
}

type communicationsStatsBucketJSON struct {
	Date  string `json:"date"`
	Value int64  `json:"value"`
}

type communicationsStatsAttentionItemJSON struct {
	Id         string    `json:"id"`
	Type       string    `json:"type"`
	Title      string    `json:"title"`
	OccurredAt time.Time `json:"occurredAt"`
	EntityId   string    `json:"entityId"`
}

// communicationsProblemJSON is the RFC 7807 shape stats' 400s alone answer
// (application/problem+json, httpx.Problem's own field set) — deliberately
// NOT commErrorJSON (channels_test.go), which is every other operation's
// {"error":{"code","message"}} shape. Decoding a stats 400 into this type
// and a non-stats 400 into commErrorJSON, in the same test, is the
// divergence pin task 9's dispatch calls for from both sides.
type communicationsProblemJSON struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Status  int    `json:"status"`
	Detail  string `json:"detail"`
	TraceID string `json:"traceId"`
}

// ---- The vocabulary divergence, pinned from both sides ----

// TestStatsErrors_AreProblemDetailsNotModuleErrorShape and
// TestNeighbouringEndpointErrors_AreModuleErrorShape together pin
// communications inventory §3 items 1-2 and the dispatch's explicit
// instruction: stats alone answers RFC 7807 ProblemDetails on
// application/problem+json (title/detail/status, no "error" wrapper, no
// machine-readable code); every other operation in this module answers
// {"error":{"code","message"}} on application/json. A mutation collapsing
// either shape into the other fails one of these two tests.
func TestStatsErrors_AreProblemDetailsNotModuleErrorShape(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-view")

	r := c.Do(http.MethodGet, "/api/v1/communications/stats/summary?from=2026-09-12T00:00:00Z&to=2026-09-01T00:00:00Z", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	if ct := r.Header("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	var problem communicationsProblemJSON
	r.JSON(&problem)
	if problem.Title != "Invalid period" {
		t.Errorf("Title = %q, want %q", problem.Title, "Invalid period")
	}
	if problem.Detail != "The 'from' value must be earlier than or equal to the 'to' value." {
		t.Errorf("Detail = %q, want the exact .NET text", problem.Detail)
	}
	if problem.Status != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", problem.Status)
	}
	// The module's own vocabulary must NOT be present alongside the RFC 7807
	// one: raw-decoding into a map and checking "error" is absent proves this
	// is not both shapes glued together.
	var raw map[string]any
	r.JSON(&raw)
	if _, ok := raw["error"]; ok {
		t.Error(`body carries an "error" key, want the bare RFC 7807 shape only`)
	}
}

// TestNeighbouringEndpointErrors_AreModuleErrorShape is the other half of
// the divergence pin above, on postCommunicationsSuppressions — a neighbour
// in this same module, deliberately not a stats endpoint.
func TestNeighbouringEndpointErrors_AreModuleErrorShape(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:suppressions-manage")

	r := c.Do(http.MethodPost, "/api/v1/communications/suppressions", map[string]any{"emailAddress": "not-an-email"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	if ct := r.Header("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json (not application/problem+json)", ct)
	}
	var body commErrorJSON
	r.JSON(&body)
	if body.Error.Code != "invalid_request" || body.Error.Message != "The request is invalid." {
		t.Errorf("error = %+v, want invalid_request / \"The request is invalid.\"", body.Error)
	}
	var raw map[string]any
	r.JSON(&raw)
	if _, ok := raw["title"]; ok {
		t.Error(`body carries a "title" key, want the module's own {"error":{...}} shape only`)
	}
}

// ---- GetCommunicationsStatsSummary ----

func TestGetCommunicationsStatsSummary_CountsWithinDefaultPeriod(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	chID := setupChannel(t, h)
	now := h.Now()
	convID := insertCommunicationsConversation(t, h, chID, "open", now, now)
	insertCommunicationsMessage(t, h, convID, now)
	h.Advance(time.Second) // the period's default "to" is now; created_at/occurred_at must fall strictly before it (":<" not "<=").

	c := h.SignIn(t, "communications:conversations-view")
	r := c.Do(http.MethodGet, "/api/v1/communications/stats/summary", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var summary communicationsStatsSummaryJSON
	r.JSON(&summary)

	// This test's own isolated database holds exactly the one conversation
	// and one message created above.
	if summary.OpenConversations != 1 {
		t.Errorf("OpenConversations = %d, want 1", summary.OpenConversations)
	}
	if summary.NewConversations != 1 {
		t.Errorf("NewConversations = %d, want 1", summary.NewConversations)
	}
	if summary.Messages != 1 {
		t.Errorf("Messages = %d, want 1", summary.Messages)
	}
	if summary.ClosedConversations != 0 {
		t.Errorf("ClosedConversations = %d, want 0", summary.ClosedConversations)
	}
	if !summary.From.Before(summary.To) {
		t.Errorf("From = %v, To = %v, want From before To", summary.From, summary.To)
	}
}

// TestGetCommunicationsStatsSummary_DefaultPeriodIsThirtyDays pins
// DefaultPeriodDays (:12) when neither from nor to is supplied.
func TestGetCommunicationsStatsSummary_DefaultPeriodIsThirtyDays(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-view")

	r := c.Do(http.MethodGet, "/api/v1/communications/stats/summary", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var summary communicationsStatsSummaryJSON
	r.JSON(&summary)

	span := summary.To.Sub(summary.From)
	want := 30 * 24 * time.Hour
	if span < want-time.Minute || span > want+time.Minute {
		t.Errorf("To - From = %v, want ~%v (30 days)", span, want)
	}
}

// TestGetCommunicationsStatsSummary_InvalidPeriodIsRejected pins
// TryNormalizePeriod's 400 (:161-173): from after to is rejected; from ==
// to is accepted (the boundary is <=, not <, per :161's own comparison).
func TestGetCommunicationsStatsSummary_InvalidPeriodIsRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-view")

	r := c.Do(http.MethodGet, "/api/v1/communications/stats/summary?from=2026-09-12T00:00:00Z&to=2026-09-01T00:00:00Z", nil)
	if r.Status != http.StatusBadRequest {
		t.Errorf("from after to: status %d body %s, want 400", r.Status, r.Body)
	}

	r2 := c.Do(http.MethodGet, "/api/v1/communications/stats/summary?from=2026-09-01T00:00:00Z&to=2026-09-01T00:00:00Z", nil)
	if r2.Status != http.StatusOK {
		t.Errorf("from == to: status %d body %s, want 200 (the boundary is <=)", r2.Status, r2.Body)
	}
}

// TestGetCommunicationsStatsSummary_OpenConversationsDeltaIsNotAPeriodDelta
// pins inventory §19 item 21: openConversationsDelta is open (the current,
// UNWINDOWED total) minus openBeforePeriod (open AND created before the
// period started) — not a period-over-period comparison like the other
// three deltas.
//
// The fixture is built so the two candidate formulas disagree: a single
// conversation created 45 days ago (inside the PREVIOUS 30-day window,
// [-60d,-30d), strictly before the period, which defaults to the last 30
// days) that is still open today.
//
//   - Correct formula (open - openBeforePeriod): open=1 (still open today),
//     openBeforePeriod=1 (created before the period starts) -> delta = 0.
//   - A plausible "period delta" mutant mirroring newConversations'/
//     messages'/closed's own pattern (openConversations created IN the
//     period minus openConversations created in the PREVIOUS period) would
//     see this row only in the previous-period term and answer -1.
//
// A mutation flipping the formula to the period-delta shape fails this
// test (0 vs -1); TestGetCommunicationsStatsSummary_CountsWithinDefaultPeriod
// above already pins the trivial case (a conversation created IN the
// period) where the two formulas happen to agree, so this fixture is
// necessary, not redundant.
func TestGetCommunicationsStatsSummary_OpenConversationsDeltaIsNotAPeriodDelta(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	chID := setupChannel(t, h)
	createdAt := h.Now().AddDate(0, 0, -45)
	insertCommunicationsConversation(t, h, chID, "open", createdAt, createdAt)

	c := h.SignIn(t, "communications:conversations-view")
	r := c.Do(http.MethodGet, "/api/v1/communications/stats/summary", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var summary communicationsStatsSummaryJSON
	r.JSON(&summary)

	if summary.OpenConversations != 1 {
		t.Fatalf("OpenConversations = %d, want 1", summary.OpenConversations)
	}
	if summary.OpenConversationsDelta != 0 {
		t.Errorf("OpenConversationsDelta = %d, want 0 (open - openBeforePeriod: 1 - 1), not -1 (a period-over-period delta would see this row only in the previous window)", summary.OpenConversationsDelta)
	}
	// This conversation was NOT created within the default period, so it
	// must not count toward newConversations either.
	if summary.NewConversations != 0 {
		t.Errorf("NewConversations = %d, want 0 (created 45 days ago, outside the default 30-day period)", summary.NewConversations)
	}
}

func TestGetCommunicationsStatsSummary_RequiresConversationsView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if r := h.SignIn(t).Do(http.MethodGet, "/api/v1/communications/stats/summary", nil); r.Status != http.StatusForbidden {
		t.Errorf("no permission: status %d, want 403", r.Status)
	}
	if r := h.SignIn(t, "communications:conversations-view").Do(http.MethodGet, "/api/v1/communications/stats/summary", nil); r.Status != http.StatusOK {
		t.Errorf("conversations-view: status %d, want 200", r.Status)
	}
}

// ---- GetCommunicationsStatsTimeseries ----

func TestGetCommunicationsStatsTimeseries_BucketsNewConversationsByDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	chID := setupChannel(t, h)
	now := h.Now()
	insertCommunicationsConversation(t, h, chID, "open", now, now)
	h.Advance(time.Second) // the period's default "to" is now; created_at must fall strictly before it.

	c := h.SignIn(t, "communications:conversations-view")
	r := c.Do(http.MethodGet, "/api/v1/communications/stats/timeseries?metric=newConversations", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var buckets []communicationsStatsBucketJSON
	r.JSON(&buckets)
	if len(buckets) != 1 || buckets[0].Value != 1 {
		t.Errorf("buckets = %+v, want exactly one bucket with value 1", buckets)
	}
}

func TestGetCommunicationsStatsTimeseries_BucketsMessagesByDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	chID := setupChannel(t, h)
	now := h.Now()
	convID := insertCommunicationsConversation(t, h, chID, "open", now, now)
	insertCommunicationsMessage(t, h, convID, now)
	insertCommunicationsMessage(t, h, convID, now)
	h.Advance(time.Second) // the period's default "to" is now; occurred_at must fall strictly before it.

	c := h.SignIn(t, "communications:conversations-view")
	r := c.Do(http.MethodGet, "/api/v1/communications/stats/timeseries?metric=messages", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var buckets []communicationsStatsBucketJSON
	r.JSON(&buckets)
	if len(buckets) != 1 || buckets[0].Value != 2 {
		t.Errorf("buckets = %+v, want exactly one bucket with value 2", buckets)
	}
}

// TestGetCommunicationsStatsTimeseries_MetricComparisonIsCaseInsensitive
// pins the dispatch's correction 3 and inventory §3.3: the comparison runs
// on the LOWERCASED value (:82-83), so "NEWCONVERSATIONS" — differently
// cased from both the error message and the contract's own
// "newConversations" — is still accepted. A case-sensitive port would be
// STRICTER than .NET and wrongly reject this input.
func TestGetCommunicationsStatsTimeseries_MetricComparisonIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-view")

	r := c.Do(http.MethodGet, "/api/v1/communications/stats/timeseries?metric=NEWCONVERSATIONS", nil)
	if r.Status != http.StatusOK {
		t.Errorf("metric=NEWCONVERSATIONS: status %d body %s, want 200", r.Status, r.Body)
	}
	r2 := c.Do(http.MethodGet, "/api/v1/communications/stats/timeseries?metric=Messages", nil)
	if r2.Status != http.StatusOK {
		t.Errorf("metric=Messages: status %d body %s, want 200", r2.Status, r2.Body)
	}
}

// TestGetCommunicationsStatsTimeseries_InvalidMetricIsProblemDetails pins
// the exact ProblemDetails text (inventory §3.3, dispatch item 2).
func TestGetCommunicationsStatsTimeseries_InvalidMetricIsProblemDetails(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-view")

	r := c.Do(http.MethodGet, "/api/v1/communications/stats/timeseries?metric=bogus", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	if ct := r.Header("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	var problem communicationsProblemJSON
	r.JSON(&problem)
	if problem.Title != "Invalid metric" {
		t.Errorf("Title = %q, want %q", problem.Title, "Invalid metric")
	}
	if problem.Detail != "Metric must be one of: newConversations, messages." {
		t.Errorf("Detail = %q, want the exact .NET text", problem.Detail)
	}
}

// TestGetCommunicationsStatsTimeseries_PeriodErrorWinsOverMetricError pins
// the ordering CommunicationsStatsEndpoints.Timeseries itself enforces
// (:81 before :82-89, inventory §2's "Stats summary/timeseries" bullet): an
// invalid period AND a bad metric together answer "Invalid period", never
// "Invalid metric".
func TestGetCommunicationsStatsTimeseries_PeriodErrorWinsOverMetricError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-view")

	r := c.Do(http.MethodGet, "/api/v1/communications/stats/timeseries?metric=bogus&from=2026-09-12T00:00:00Z&to=2026-09-01T00:00:00Z", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem communicationsProblemJSON
	r.JSON(&problem)
	if problem.Title != "Invalid period" {
		t.Errorf("Title = %q, want %q (period check must run before the metric check)", problem.Title, "Invalid period")
	}
}

func TestGetCommunicationsStatsTimeseries_RequiresConversationsView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if r := h.SignIn(t).Do(http.MethodGet, "/api/v1/communications/stats/timeseries?metric=messages", nil); r.Status != http.StatusForbidden {
		t.Errorf("no permission: status %d, want 403", r.Status)
	}
}

// ---- GetCommunicationsStatsAttention ----

func TestGetCommunicationsStatsAttention_EmptyBareArray(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-view")

	r := c.Do(http.MethodGet, "/api/v1/communications/stats/attention", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got []communicationsStatsAttentionItemJSON
	r.JSON(&got)
	if got == nil {
		t.Error("body decoded to nil, want a bare [] array")
	}
	if len(got) != 0 {
		t.Errorf("items = %v, want empty", got)
	}
}

// TestGetCommunicationsStatsAttention_IncludesBothFailedStatuses pins the
// status filter (:121): both "failed" and "submission_failed" qualify, and
// each item's shape is failedDelivery/"Message delivery failed"/the
// message's own id as entityId (:122-129).
func TestGetCommunicationsStatsAttention_IncludesBothFailedStatuses(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	chID := setupChannel(t, h)
	now := h.Now()
	convID := insertCommunicationsConversation(t, h, chID, "open", now, now)
	messageID := insertCommunicationsMessage(t, h, convID, now)
	failedID := insertCommunicationsDelivery(t, h, messageID, "failed", now)
	submissionFailedID := insertCommunicationsDelivery(t, h, messageID, "submission_failed", now)
	// A queued delivery must NOT appear.
	insertCommunicationsDelivery(t, h, messageID, "queued", now)

	c := h.SignIn(t, "communications:conversations-view")
	r := c.Do(http.MethodGet, "/api/v1/communications/stats/attention", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var items []communicationsStatsAttentionItemJSON
	r.JSON(&items)
	if len(items) != 2 {
		t.Fatalf("items = %+v, want exactly 2 (failed + submission_failed, not queued)", items)
	}
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Id] = true
		if item.Type != "failedDelivery" {
			t.Errorf("item %s: Type = %q, want failedDelivery", item.Id, item.Type)
		}
		if item.Title != "Message delivery failed" {
			t.Errorf("item %s: Title = %q, want %q", item.Id, item.Title, "Message delivery failed")
		}
		if item.EntityId != messageID.String() {
			t.Errorf("item %s: EntityId = %q, want the message id %q", item.Id, item.EntityId, messageID.String())
		}
	}
	if !seen[failedID.String()] || !seen[submissionFailedID.String()] {
		t.Errorf("items = %+v, want both %q and %q present", items, failedID, submissionFailedID)
	}
}

// TestGetCommunicationsStatsAttention_OrdersOldestFirst is this task's
// central mutation-catcher (dispatch: "the instinct to sort newest-first is
// the failure mode here"): CommunicationsStatsEndpoints.Attention orders
// OccurredAt ASCENDING (:147) then Take(100) — the hundred OLDEST items,
// not the newest. Two failed deliveries with distinguishable created_at; a
// mutation flipping ASC to DESC fails this test.
func TestGetCommunicationsStatsAttention_OrdersOldestFirst(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	chID := setupChannel(t, h)
	now := h.Now()
	convID := insertCommunicationsConversation(t, h, chID, "open", now, now)
	messageID := insertCommunicationsMessage(t, h, convID, now)
	older := insertCommunicationsDelivery(t, h, messageID, "failed", now.Add(-2*time.Hour))
	newer := insertCommunicationsDelivery(t, h, messageID, "submission_failed", now.Add(-1*time.Hour))

	c := h.SignIn(t, "communications:conversations-view")
	r := c.Do(http.MethodGet, "/api/v1/communications/stats/attention", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var items []communicationsStatsAttentionItemJSON
	r.JSON(&items)
	if len(items) != 2 {
		t.Fatalf("items = %+v, want exactly 2", items)
	}
	if items[0].Id != older.String() || items[1].Id != newer.String() {
		t.Errorf("order = [%s, %s], want [%s, %s] (oldest first, ascending occurredAt)",
			items[0].Id, items[1].Id, older, newer)
	}
	if !items[0].OccurredAt.Before(items[1].OccurredAt) {
		t.Errorf("items[0].OccurredAt = %v, items[1].OccurredAt = %v, want the first strictly before the second", items[0].OccurredAt, items[1].OccurredAt)
	}
}

// TestGetCommunicationsStatsAttention_FailedDeliveriesAreUnboundedInTime
// pins that the failed-delivery half of the query has NO time filter
// (:120-121 — unlike unanswered's last_activity_at < cutoff half): a
// delivery that failed a year ago still shows up today.
func TestGetCommunicationsStatsAttention_FailedDeliveriesAreUnboundedInTime(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	chID := setupChannel(t, h)
	now := h.Now()
	convID := insertCommunicationsConversation(t, h, chID, "open", now, now)
	messageID := insertCommunicationsMessage(t, h, convID, now)
	ancient := insertCommunicationsDelivery(t, h, messageID, "failed", now.AddDate(-1, 0, 0))

	c := h.SignIn(t, "communications:conversations-view")
	r := c.Do(http.MethodGet, "/api/v1/communications/stats/attention", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var items []communicationsStatsAttentionItemJSON
	r.JSON(&items)
	found := false
	for _, item := range items {
		if item.Id == ancient.String() {
			found = true
		}
	}
	if !found {
		t.Errorf("items = %+v, want the year-old failed delivery %q present (unbounded in time)", items, ancient)
	}
}

// TestGetCommunicationsStatsAttention_NeverIncludesOpenConversationsWhoseNewestMessageIsNotInbound
// pins the "unanswered open conversation" half of the union (:131-144): it
// requires the conversation's NEWEST message to be inbound. An old, open
// conversation whose only message is outbound must never qualify.
//
// Task 7 fix round 2 correction: an earlier version of this test (then
// named ...NeverIncludesOpenConversationsWithoutInboundMessages) claimed
// this arm was structurally *impossible* to reach at all — that no fixture
// could ever produce a qualifying row, because
// conversation_messages.direction's CHECK constraint then admitted only
// 'outbound' and 'internal_note'. That CHECK narrowing was itself a mistake
// (design doc §1.1's correction) and has been widened to match .NET's full
// Direction domain (inbound included), so the arm *is* reachable from a
// fixture now — see the companion test below, which proves it. This test
// keeps its narrower, still-true claim: an outbound-only conversation does
// not qualify, regardless of what the schema permits elsewhere.
func TestGetCommunicationsStatsAttention_NeverIncludesOpenConversationsWhoseNewestMessageIsNotInbound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	chID := setupChannel(t, h)
	old := h.Now().Add(-48 * time.Hour)
	staleID := insertCommunicationsConversation(t, h, chID, "open", old, old)
	// Also give it an outbound message — still not inbound, still must not
	// qualify.
	insertCommunicationsMessage(t, h, staleID, old)

	c := h.SignIn(t, "communications:conversations-view")
	r := c.Do(http.MethodGet, "/api/v1/communications/stats/attention", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var items []communicationsStatsAttentionItemJSON
	r.JSON(&items)
	for _, item := range items {
		if item.Id == staleID.String() || item.Type == "conversationNoReply" {
			t.Errorf("items = %+v, want no conversationNoReply items (this conversation's newest message is outbound, not inbound)", items)
		}
	}
}

// TestGetCommunicationsStatsAttention_IncludesOpenConversationsWithAnInboundNewestMessage
// is the companion the test above's comment promises: an old, open
// conversation whose newest message is genuinely inbound (a fixture, per
// this port's outbound-only reality — design doc §1.1) DOES appear as a
// conversationNoReply item. This is the arm's live branch, reachable only
// because task 7 fix round 2 widened conversation_messages.direction's
// CHECK to match .NET's full Direction domain; before that, no fixture
// could construct this row at all and the arm was untestable on its
// success path, not merely unreached in production.
func TestGetCommunicationsStatsAttention_IncludesOpenConversationsWithAnInboundNewestMessage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	chID := setupChannel(t, h)
	old := h.Now().Add(-48 * time.Hour)
	convID := insertCommunicationsConversation(t, h, chID, "open", old, old)
	insertCommunicationsInboundMessage(t, h, convID, old)

	c := h.SignIn(t, "communications:conversations-view")
	r := c.Do(http.MethodGet, "/api/v1/communications/stats/attention", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var items []communicationsStatsAttentionItemJSON
	r.JSON(&items)
	found := false
	for _, item := range items {
		if item.Id == convID.String() && item.Type == "conversationNoReply" {
			found = true
		}
	}
	if !found {
		t.Errorf("items = %+v, want a conversationNoReply item for %s (open, stale, newest message inbound)", items, convID)
	}
}

func TestGetCommunicationsStatsAttention_RequiresConversationsView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if r := h.SignIn(t).Do(http.MethodGet, "/api/v1/communications/stats/attention", nil); r.Status != http.StatusForbidden {
		t.Errorf("no permission: status %d, want 403", r.Status)
	}
	if r := h.SignIn(t, "communications:conversations-view").Do(http.MethodGet, "/api/v1/communications/stats/attention", nil); r.Status != http.StatusOK {
		t.Errorf("conversations-view: status %d, want 200", r.Status)
	}
}
