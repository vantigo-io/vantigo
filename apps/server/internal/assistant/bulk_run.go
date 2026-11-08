package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/agentloop"
	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
	"github.com/vantigo-io/vantigo/server/internal/assistant/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// Where a bulk job runs, and how.
//
// The dispatcher is built and bound only by module.Compose, in a process that
// serves the API; module.Workers (worker mode, and the workers of an API
// process) hands a module's workers Deps with no dispatcher. So a job cannot run
// in a worker: it runs in the API process, as a goroutine of the replica that
// served the person's Start or Continue, under a claim on its row. The claim
// (claimed_by, claimed_until) is renewed by every item, which the same statement
// counts; a claim that lapsed (the replica crashed or was redeployed) is taken
// over by the next replica that sees the job — on the person's own read of it,
// or from the stale-runs worker when the workers run in the API process — and
// the job goes on from its next pending item, once.
//
// An item runs at most once (S17). Before its call the runner marks it
// in_flight in a statement that commits on its own; the statement that takes a
// lapsed job over moves every in_flight item to unknown, counts it processed
// and never calls it: the dead replica may or may not have made the change.
//
// No statement of a job holds a transaction or a connection across a call
// through the dispatcher: the calls are made between statements, each of which is
// one round trip on the pool. A job on a pool of one connection therefore runs.

// bulkHosts maps a database pool to the assistant server composed over it, so a
// worker built over the same pool can hand a lapsed job to the API that can run
// it. (A worker-mode process has no entry, and only expires.)
var bulkHosts sync.Map // *pgxpool.Pool -> *server

func (s *server) registerBulkHost() {
	if s.deps.Pool != nil {
		bulkHosts.Store(s.deps.Pool, s)
	}
}

// resumeLapsedFor hands the lapsed jobs to the API server composed over d's pool,
// if this process has one.
func resumeLapsedFor(ctx context.Context, d module.Deps) {
	if v, ok := bulkHosts.Load(d.Pool); ok {
		v.(*server).resumeLapsed(ctx, uuid.Nil)
	}
}

// bulkOps is the dispatcher, or the scripted one a test installed.
func (s *server) bulkOps() bulkOps {
	if s.opsOverride != nil {
		return s.opsOverride
	}
	if s.deps.Operations == nil {
		return nil
	}
	return s.deps.Operations
}

// claimedBy is this replica's name on the claims it holds.
func (s *server) claimedBy() *string { return &s.claimID }

// -- storing a plan ---------------------------------------------------------

func itemJSON(it bulkItem) []byte {
	raw, _ := json.Marshal(it)
	return raw
}

// createBulkPending stores a bulk plan: the pending action, the job and its
// frozen items, in one transaction that calls nothing else.
func (r *runner) createBulkPending(d *pendingDraft, callID string, toolMessage uuid.UUID) (store.AssistantPendingAction, error) {
	request, err := json.Marshal(d.request)
	if err != nil {
		return store.AssistantPendingAction{}, err
	}
	preview, err := json.Marshal(d.preview)
	if err != nil {
		return store.AssistantPendingAction{}, err
	}
	selector, err := json.Marshal(d.bulk.selector)
	if err != nil {
		return store.AssistantPendingAction{}, err
	}
	template, err := json.Marshal(d.bulk.template)
	if err != nil {
		return store.AssistantPendingAction{}, err
	}
	n := len(d.bulk.items) + len(d.bulk.excluded)
	seqs, items, statuses, codes := make([]int32, 0, n), make([][]byte, 0, n), make([]string, 0, n), make([]string, 0, n)
	for _, it := range d.bulk.items {
		seqs, items, statuses, codes = append(seqs, int32(len(seqs))), append(items, itemJSON(it)), append(statuses, "pending"), append(codes, "")
	}
	for _, ex := range d.bulk.excluded {
		seqs, items, statuses, codes = append(seqs, int32(len(seqs))), append(items, itemJSON(ex.item)), append(statuses, "skipped"), append(codes, ex.reason)
	}

	ctx, now := r.persist(), r.s.deps.Clock()
	var row store.AssistantPendingAction
	err = db.WithTx(ctx, r.s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		row, err = q.InsertPendingAction(ctx, store.InsertPendingActionParams{
			ID: uuid.New(), RunID: r.runID, ConversationID: r.convID, UserID: r.userID, OperationID: d.request.OperationID,
			CallID: callID, ToolMessageID: &toolMessage, Request: request, Preview: preview, Now: now, ExpiresAt: now.Add(pendingTTL), Kind: kindBulk,
		})
		if err != nil {
			return err
		}
		job, err := q.InsertBulkJob(ctx, store.InsertBulkJobParams{
			ID: uuid.New(), PendingActionID: row.ID, RunID: r.runID, ConversationID: r.convID, UserID: r.userID,
			OperationID: d.bulk.template.OperationID, Selector: selector, Template: template,
			Total: int32(n), Skipped: int32(len(d.bulk.excluded)), Now: now,
		})
		if err != nil {
			return err
		}
		return q.InsertBulkItems(ctx, store.InsertBulkItemsParams{JobID: job.ID, Seqs: seqs, Items: items, Statuses: statuses, Codes: codes})
	})
	return row, err
}

// -- the views --------------------------------------------------------------

func bulkItemView(it store.AssistantBulkJobItem) gen.AssistantBulkItem {
	var item bulkItem
	_ = json.Unmarshal(it.Item, &item)
	out := gen.AssistantBulkItem{Seq: it.Seq, Status: gen.AssistantBulkItemStatus(it.Status), HttpStatus: it.HttpStatus, Code: it.ProblemCode, Title: it.ProblemTitle}
	if item.Label != "" {
		label := item.Label
		out.Label = &label
	}
	return out
}

func bulkItemViews(rows []store.AssistantBulkJobItem) []gen.AssistantBulkItem {
	out := make([]gen.AssistantBulkItem, 0, len(rows))
	for _, it := range rows {
		out = append(out, bulkItemView(it))
	}
	return out
}

// bulkPlanView is the plan as its card shows it: the preview the card was made
// with, and where the job stands.
func bulkPlanView(preview []byte, job store.AssistantBulkJob, canary []store.AssistantBulkJobItem) gen.AssistantBulkPlan {
	var doc previewDoc
	_ = json.Unmarshal(preview, &doc)
	plan := gen.AssistantBulkPlan{
		JobId: job.ID, State: gen.AssistantBulkPlanState(job.State), Total: job.Total, Processed: job.Processed,
		Succeeded: job.Succeeded, Failed: job.Failed, Unknown: job.Unknown, Skipped: job.Skipped, CanarySize: bulkCanarySize,
		Canary: bulkItemViews(canary), Sample: []gen.AssistantBulkSample{}, Excluded: gen.AssistantBulkExcluded{Reasons: []gen.AssistantBulkReason{}},
		PauseReason: job.PauseReason, ErrorCode: job.ErrorCode, StartedAt: job.StartedAt, EndedAt: job.EndedAt,
	}
	if job.GrantExpiresAt != nil && (job.State == jobCanary || job.State == jobRunning || job.State == jobStopping || job.State == jobAwaiting) {
		plan.GrantExpiresAt = job.GrantExpiresAt
	}
	if doc.Bulk != nil {
		if doc.Bulk.Sample != nil {
			plan.Sample = doc.Bulk.Sample
		}
		plan.Excluded = doc.Bulk.Excluded
		if plan.Excluded.Reasons == nil {
			plan.Excluded.Reasons = []gen.AssistantBulkReason{}
		}
	}
	return plan
}

// bulkJobView is the job: the plan and its first failures.
func bulkJobView(plan gen.AssistantBulkPlan, failures []store.AssistantBulkJobItem) gen.AssistantBulkJob {
	return gen.AssistantBulkJob{
		Canary: plan.Canary, CanarySize: plan.CanarySize, EndedAt: plan.EndedAt, ErrorCode: plan.ErrorCode, Excluded: plan.Excluded,
		Failed: plan.Failed, Unknown: plan.Unknown, Failures: bulkItemViews(failures), GrantExpiresAt: plan.GrantExpiresAt, JobId: plan.JobId,
		PauseReason: plan.PauseReason, Processed: plan.Processed, Sample: plan.Sample, Skipped: plan.Skipped, StartedAt: plan.StartedAt,
		State: gen.AssistantBulkJobState(plan.State), Succeeded: plan.Succeeded, Total: plan.Total,
	}
}

// cardOf is a pending action as the interface shows it: a bulk plan carries its
// job. It reads the job on the pool, so callers hold no connection of their own.
func (s *server) cardOf(ctx context.Context, row store.AssistantPendingAction) gen.AssistantPendingAction {
	card := pendingView(row, s.deps.Clock())
	if row.Kind != kindBulk {
		return card
	}
	q := store.New(s.deps.Pool)
	job, err := q.GetBulkJobByAction(ctx, store.GetBulkJobByActionParams{PendingActionID: row.ID, UserID: row.UserID})
	if err != nil {
		return card
	}
	var canary []store.AssistantBulkJobItem
	if job.State != jobPreviewed {
		canary, _ = q.ListBulkCanary(ctx, store.ListBulkCanaryParams{JobID: job.ID, Below: bulkCanarySize})
	}
	plan := bulkPlanView(row.Preview, job, canary)
	card.Bulk = &plan
	return card
}

// -- running ----------------------------------------------------------------

// itemFate is what happened to one item.
type itemFate int

const (
	fateDone      itemFate = iota // recorded as succeeded, failed or skipped
	fateSession                   // the session ended: the job stops here
	fateNarrowed                  // the Owner narrowed what the assistant may use
	fateCancelled                 // the process is going away or the conversation was deleted
)

type itemResult struct {
	fate   itemFate
	status string // succeeded, failed or unknown
	http   *int32
	code   *string
	title  *string
}

func ptr[T any](v T) *T { return &v }

// problemFields are the code and title of a refusal's problem body, and nothing
// else of it: never the body.
func problemFields(status int, body json.RawMessage) (code, title string) {
	if m, ok := decodeObject(body); ok {
		if s, ok := m["code"].(string); ok {
			code = clip(s, 120)
		}
		if s, ok := m["title"].(string); ok {
			title = clip(s, 300)
		}
	}
	if code == "" {
		code = fmt.Sprintf("http_%d", status)
	}
	if title == "" {
		title = http.StatusText(status)
	}
	return code, title
}

// idempotencyKeyHeader is the header an operation declares to make a retry of a
// change safe.
const idempotencyKeyHeader = "Idempotency-Key"

// declaresIdempotencyKey says whether the operation declares an Idempotency-Key
// header parameter, as its description lists it.
func declaresIdempotencyKey(ops bulkOps, operationID string) bool {
	raw, err := ops.Describe(operationID)
	if err != nil {
		return false
	}
	var doc struct {
		Headers []struct {
			Name string `json:"name"`
		} `json:"headerParameters"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return false
	}
	for _, h := range doc.Headers {
		if strings.EqualFold(h.Name, idempotencyKeyHeader) {
			return true
		}
	}
	return false
}

// itemKey is the idempotency key of one item of a job: built from the job's
// parsed id and the item's position, never from text.
func itemKey(job uuid.UUID, seq int32) string {
	return fmt.Sprintf("assistant-bulk-%s-%d", job.String(), seq)
}

// unknownItem is an item whose call ended in a way that does not say whether
// the change was made: a handler that panicked or ran out of time part-way.
func unknownItem(code string) itemResult {
	return itemResult{status: "unknown", code: ptr(code), title: ptr(unknownItemSentence)}
}

// runItem makes the change for one item through the dispatcher, as the person.
// keyed says the operation declares an Idempotency-Key, which the item then
// sends.
func (s *server) runItem(ctx context.Context, ops bulkOps, tpl bulkTemplate, job uuid.UUID, keyed bool, it store.AssistantBulkJobItem) itemResult {
	var item bulkItem
	if err := json.Unmarshal(it.Item, &item); err != nil {
		return itemResult{status: "failed", code: ptr("bad_item"), title: ptr("the item could not be read")}
	}
	req := tpl.request(item.Bound)
	req.Confirmed = true
	if keyed {
		req.Headers = map[string]string{idempotencyKeyHeader: itemKey(job, it.Seq)}
	}
	res, timedOut, err := callWithRetry(ctx, ops, req)
	if err != nil {
		if ctx.Err() != nil {
			return itemResult{fate: fateCancelled}
		}
		var oe *operations.Error
		if errors.As(err, &oe) {
			switch oe.Code {
			case operations.CodeNever:
				return itemResult{fate: fateNarrowed}
			case operations.CodeCallFailed:
				// The handler panicked part-way: what it wrote before it did may
				// stand (S17).
				return unknownItem("call_failed")
			}
			return itemResult{status: "failed", code: ptr(clip(oe.Code, 120)), title: ptr(clip(oe.Message, 300))}
		}
		return itemResult{status: "failed", code: ptr("call_failed"), title: ptr("the change failed unexpectedly")}
	}
	status := int32(res.Status)
	switch {
	case res.Status >= 200 && res.Status < 300:
		return itemResult{status: "succeeded", http: &status}
	case timedOut:
		// Cut off by the item's own deadline: whatever the handler answered, it
		// may have committed first (S17).
		return unknownItem("timed_out")
	case res.Status == http.StatusUnauthorized:
		// The grant stands on the person's session: a 401 is the session having
		// ended (they signed out), and nothing after it can run.
		return itemResult{fate: fateSession}
	}
	code, title := problemFields(res.Status, res.Body)
	return itemResult{status: "failed", http: &status, code: &code, title: &title}
}

// runBulk runs a job this replica holds the claim of: the canary when it is in
// that state, else the chunks, until it ends, pauses or loses its claim. emit,
// when given, is told of the job after every item (the Start request's stream).
func (s *server) runBulk(ctx context.Context, job store.AssistantBulkJob, emit func(store.AssistantBulkJob)) {
	ops := s.bulkOps()
	if ops == nil {
		return
	}
	q := store.New(s.deps.Pool)
	persist := context.WithoutCancel(ctx)
	var tpl bulkTemplate
	if err := json.Unmarshal(job.Template, &tpl); err != nil {
		s.endBulk(persist, job, jobFailed, reasonInternal)
		return
	}
	keyed := declaresIdempotencyKey(ops, tpl.OperationID)
	canary := job.State == jobCanary
	below := int32(math.MaxInt32)
	if canary {
		below = bulkCanarySize
	}

	for {
		now := s.deps.Clock()
		cur, err := q.TouchBulkClaim(persist, store.TouchBulkClaimParams{ID: job.ID, ClaimedBy: s.claimedBy(), ClaimedUntil: now.Add(bulkClaimTTL), Now: now})
		if errors.Is(err, pgx.ErrNoRows) {
			return // the claim is no longer ours, or the job is over
		}
		if err != nil {
			s.deps.Logger.Error("assistant: renew a job's claim", "job", job.ID, "error", err)
			return
		}
		if ctx.Err() != nil {
			return // the conversation was deleted, or the process is going away
		}
		if cur.StopRequested || cur.State == jobStopping {
			s.endBulk(persist, cur, jobStopped, reasonByPerson)
			return
		}
		if !canary {
			// Between chunks: the Owner may have narrowed what the assistant may use,
			// and the grant must outlive the next chunk.
			if mode, ok := ops.Effective(ctx, cur.OperationID); !ok || mode != operations.ModeConfirm {
				s.endBulk(persist, cur, jobStopped, reasonNarrowed)
				return
			}
			if cur.GrantExpiresAt == nil || cur.GrantExpiresAt.Sub(now) < bulkGrantMargin {
				s.pauseBulk(persist, cur, reasonGrantLapse)
				return
			}
		}

		chunk, err := q.ListPendingBulkItems(persist, store.ListPendingBulkItemsParams{JobID: job.ID, Below: below, BatchSize: bulkChunkSize})
		if err != nil {
			s.deps.Logger.Error("assistant: read a job's next items", "job", job.ID, "error", err)
			return
		}
		if len(chunk) == 0 {
			if !canary {
				s.endBulk(persist, cur, jobDone, "")
				return
			}
			more, err := q.CountPendingBulkItems(persist, job.ID)
			switch {
			case err != nil:
				s.deps.Logger.Error("assistant: count a job's items", "job", job.ID, "error", err)
			case more == 0:
				s.endBulk(persist, cur, jobDone, "")
			default:
				s.pauseBulk(persist, cur, reasonCanaryDone)
			}
			return
		}
		for _, it := range chunk {
			if ctx.Err() != nil {
				return
			}
			// The item is in flight, in a statement of its own that commits before
			// the call goes out and holds nothing across it: a replica that dies
			// from here on leaves the item for the next one to mark unknown, and
			// nothing calls it twice (S17).
			now = s.deps.Clock()
			if _, err := q.MarkBulkItemInFlight(persist, store.MarkBulkItemInFlightParams{JobID: job.ID, Seq: it.Seq, ClaimedBy: s.claimedBy(), Now: now}); err != nil {
				if !errors.Is(err, pgx.ErrNoRows) { // else another replica holds the job now
					s.deps.Logger.Error("assistant: mark an item in flight", "job", job.ID, "error", err)
				}
				return
			}
			if s.afterInFlight != nil && s.afterInFlight(job.ID, it.Seq) {
				return // a test's replica dying with the call not yet made
			}
			res := s.runItem(ctx, ops, tpl, job.ID, keyed, it)
			switch res.fate {
			case fateSession, fateNarrowed:
				// The call was refused before it could change anything.
				if err := q.ReleaseBulkItem(persist, store.ReleaseBulkItemParams{JobID: job.ID, Seq: it.Seq, ClaimedBy: s.claimedBy()}); err != nil {
					s.deps.Logger.Error("assistant: release an item", "job", job.ID, "error", err)
				}
				if res.fate == fateSession {
					s.endBulk(persist, cur, jobStopped, reasonSessionEnd)
				} else {
					s.endBulk(persist, cur, jobStopped, reasonNarrowed)
				}
				return
			case fateCancelled:
				return // the call may have gone out: the item stays in flight
			}
			now = s.deps.Clock()
			rec, err := q.RecordBulkItem(persist, store.RecordBulkItemParams{
				JobID: job.ID, Seq: it.Seq, ClaimedBy: s.claimedBy(), Status: res.status, HttpStatus: res.http,
				ProblemCode: res.code, ProblemTitle: res.title, ClaimedUntil: now.Add(bulkClaimTTL), Now: now,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return // another replica holds the job now
			}
			if err != nil {
				s.deps.Logger.Error("assistant: record an item", "job", job.ID, "error", err)
				return
			}
			if emit != nil {
				emit(rec)
			}
		}
	}
}

// pauseBulk has the job wait for the person's Continue.
func (s *server) pauseBulk(ctx context.Context, job store.AssistantBulkJob, reason string) {
	paused, err := store.New(s.deps.Pool).PauseBulkJob(ctx, store.PauseBulkJobParams{ID: job.ID, ClaimedBy: s.claimedBy(), PauseReason: &reason, Now: s.deps.Clock()})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.deps.Logger.Error("assistant: pause a job", "job", job.ID, "error", err)
		return
	}
	_ = paused
}

// bulkBackground is the context a job runs in: the person and the job's grant,
// cancelled by the registry when the conversation is deleted.
func (s *server) bulkBackground(job store.AssistantBulkJob, ceiling time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(context.Background())
	ctx, stopTimer := context.WithTimeoutCause(ctx, ceiling, errors.New("assistant: the bulk job reached its ceiling"))
	s.runs.add(job.RunID, job.ConversationID, func(cause error) { cancel(cause) })
	grant := uuid.Nil
	if job.GrantID != nil {
		grant = *job.GrantID
	}
	ctx = withGrant(ctx, job.UserID, grant)
	return ctx, func() {
		stopTimer()
		cancel(nil)
		s.runs.remove(job.RunID)
	}
}

// bulkCeiling is how long one stretch of a job may run: the grant's life and
// some.
const bulkCeiling = bulkGrantTTL + 10*time.Minute

// startBulk runs a job this replica holds the claim of, on a goroutine.
func (s *server) startBulk(job store.AssistantBulkJob, emit func(store.AssistantBulkJob), after func()) {
	ctx, release := s.bulkBackground(job, bulkCeiling)
	go func() {
		defer release()
		s.runBulk(ctx, job, emit)
		if after != nil {
			after()
		}
	}()
}

// resumeBulk takes a job whose claim lapsed and runs it from its next pending
// item. Exactly one replica wins the claim.
func (s *server) resumeBulk(job store.AssistantBulkJob) {
	now := s.deps.Clock()
	claimed, err := store.New(s.deps.Pool).ClaimBulkJob(context.Background(), store.ClaimBulkJobParams{ID: job.ID, ClaimedBy: s.claimedBy(), ClaimedUntil: now.Add(bulkClaimTTL), Now: now})
	if err != nil {
		return // taken by another replica, or over
	}
	if claimed.GrantID == nil {
		s.endBulk(context.Background(), claimed, jobFailed, reasonInternal)
		return
	}
	s.startBulk(claimed, nil, nil)
}

// resumeLapsed takes over the running jobs nobody holds the claim of: one
// person's, or all of them for uuid.Nil.
func (s *server) resumeLapsed(ctx context.Context, user uuid.UUID) {
	if s.bulkOps() == nil {
		return
	}
	var who *uuid.UUID
	if user != uuid.Nil {
		who = &user
	}
	jobs, err := store.New(s.deps.Pool).ListLapsedBulkJobs(ctx, store.ListLapsedBulkJobsParams{Now: s.deps.Clock(), UserID: who, BatchSize: 20})
	if err != nil {
		s.deps.Logger.Error("assistant: find the lapsed jobs", "error", err)
		return
	}
	for _, job := range jobs {
		s.resumeBulk(job)
	}
}

// -- the end of a job -------------------------------------------------------

// bulkSummary is what the model reads when a job ends: the counts and the first
// failures, never a value.
func bulkSummary(job store.AssistantBulkJob, failures []store.AssistantBulkJobItem) string {
	type failure struct {
		Item   string `json:"item,omitempty"`
		Status *int32 `json:"status,omitempty"`
		Code   string `json:"code,omitempty"`
		Title  string `json:"title,omitempty"`
	}
	first := []failure{}
	for i, f := range failures {
		if i == 5 {
			break
		}
		v := bulkItemView(f)
		fl := failure{Status: v.HttpStatus}
		if v.Label != nil {
			fl.Item = *v.Label
		}
		if v.Code != nil {
			fl.Code = *v.Code
		}
		if v.Title != nil {
			fl.Title = *v.Title
		}
		first = append(first, fl)
	}
	body := map[string]any{
		"bulk": true, "state": job.State, "total": job.Total, "succeeded": job.Succeeded, "failed": job.Failed, "unknown": job.Unknown, "skipped": job.Skipped,
		"notRun":        job.Total - job.Processed,
		"firstFailures": first,
		"note":          "The bulk change has ended. Sum it up for the person in one or two short sentences: how many items were changed, how many failed and the commonest reason, how many are unknown (the change may or may not have been made, and the person should check them), and if it stopped before the end, that and why. The card shows the failed items with a download, so do not list them. Never claim more was changed than succeeded says; an unknown item is not a success.",
	}
	if job.ErrorCode != nil {
		body["endedBecause"] = *job.ErrorCode
	}
	return agentloop.Wrap(body)
}

// endBulk ends a job once: its row (redacted), its pending action, the waiting
// tool result (replaced by the summary), its grant, and the run, which resumes
// to tell the person in one short message. A job ended by someone else, or
// already over, is left alone. The job's end, the card's outcome and the tool
// result commit together: nobody reads a finished job whose card has no
// outcome, and a replica that dies between them leaves neither.
func (s *server) endBulk(ctx context.Context, job store.AssistantBulkJob, state, code string) {
	ctx = context.WithoutCancel(ctx)
	q := store.New(s.deps.Pool)
	var errorCode *string
	if code != "" {
		errorCode = &code
	}
	now := s.deps.Clock()

	// The card: confirmed once it started, failed when it broke.
	status, ok := "confirmed", state == jobDone
	outcome := gen.AssistantPendingOutcome{Ok: ok}
	if code != "" {
		outcome.Reason = &code
	}
	if state == jobFailed {
		status = "failed"
	}
	outcomeJSON, _ := json.Marshal(outcome)

	var ended store.AssistantBulkJob
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		tq := store.New(tx)
		var err error
		if ended, err = tq.EndBulkJob(ctx, store.EndBulkJobParams{ID: job.ID, State: state, ErrorCode: errorCode, Now: now, ClaimedBy: s.claimedBy()}); err != nil {
			return err
		}
		finished, err := tq.FinishPendingAction(ctx, store.FinishPendingActionParams{ID: ended.PendingActionID, Status: status, Outcome: outcomeJSON, Now: now})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // the card was settled already
		}
		if err != nil {
			return fmt.Errorf("record the card's outcome: %w", err)
		}
		// The waiting tool result becomes the outcome, so the model's transcript
		// reads what happened where it asked for it.
		if finished.ToolMessageID == nil {
			return nil
		}
		failures, err := tq.ListBulkFailures(ctx, store.ListBulkFailuresParams{JobID: ended.ID, BatchSize: maxBulkFailuresShown})
		if err != nil {
			return fmt.Errorf("read the job's failures: %w", err)
		}
		callID, name, st, content := finished.CallID, toolProposeBulk, gen.AssistantPartStatus("ok"), bulkSummary(ended, failures)
		if state == jobFailed {
			st = gen.AssistantPartStatus("error")
		}
		part := gen.AssistantPart{Type: gen.AssistantPartType("tool_result"), CallId: &callID, Name: &name, Status: &st, Content: &content}
		if err := tq.UpdateMessageContent(ctx, store.UpdateMessageContentParams{ID: *finished.ToolMessageID, Content: marshalParts([]gen.AssistantPart{part})}); err != nil {
			return fmt.Errorf("settle the tool result: %w", err)
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		s.deps.Logger.Error("assistant: end a job", "job", job.ID, "error", err)
		return
	}
	if ended.GrantID != nil && s.deps.AssistantGrants != nil {
		revoke, cancel := context.WithTimeout(ctx, 10*time.Second)
		_ = s.deps.AssistantGrants.Revoke(revoke, *ended.GrantID)
		cancel()
	}
	s.countChange(ctx, ended.UserID, ok)

	// The run that waited goes on: the model sums the outcome up, without tools.
	if _, err := q.ResumeRun(ctx, store.ResumeRunParams{ID: ended.RunID, UserID: ended.UserID, Now: now}); err != nil {
		return // the run was stopped or the conversation deleted meanwhile
	}
	go s.summariseBulk(ended)
}

// summariseBulk is the resumed run after a job: one short answer from the
// model, persisted for the conversation to show, and streamed to nobody (the
// panel polls). It uses no tools and no dispatcher.
func (s *server) summariseBulk(job store.AssistantBulkJob) {
	ctx := context.Background()
	lang, name := s.person(ctx, job.UserID)
	runCtx, release := s.bulkBackground(job, runCeiling)
	defer release()
	queue := newEventQueue()
	queue.abandon()
	grant := uuid.Nil
	if job.GrantID != nil {
		grant = *job.GrantID
	}
	r := &runner{
		s: s, ctx: runCtx, runID: job.RunID, convID: job.ConversationID, userID: job.UserID, grantID: grant, queue: queue, lang: lang, name: name,
		tools: s.newToolbox(runCtx, job.UserID, lang),
		final: true,
	}
	r.execute()
}

// -- the person's Start -----------------------------------------------------

// decideBulk is the person's decision on a bulk plan. Start (confirm) claims
// the action once, issues the job's grant from this request's session and runs
// the canary — the first five items, for real — on a detached goroutine,
// streaming the card after each item; the run stays paused, because the job is
// what it waits for. Cancel is B's: the model is told, and answers in a line.
func (s *server) decideBulk(ctx context.Context, action store.AssistantPendingAction, confirm bool) (*streamResponse, *refusal, error) {
	user := callerID(ctx)
	q := store.New(s.deps.Pool)
	ops := s.bulkOps()
	if confirm {
		// The Owner may have narrowed what the assistant may use while the card
		// waited; a change the gate now refuses is not made.
		if mode, ok := ops.Effective(ctx, action.OperationID); !ok || mode != operations.ModeConfirm {
			r := refuse(403, codeNotAllowed, "The assistant may no longer make this change")
			return nil, &r, nil
		}
	}
	conv, err := q.GetConversation(ctx, store.GetConversationParams{ID: action.ConversationID, UserID: user})
	if errors.Is(err, pgx.ErrNoRows) {
		r := notFound()
		return nil, &r, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("assistant: read the conversation: %w", err)
	}
	if !confirm {
		return s.cancelBulk(ctx, action, conv)
	}

	now := s.deps.Clock()
	grantID, err := s.deps.AssistantGrants.Issue(ctx, conv.ID, action.RunID, bulkGrantTTL)
	if err != nil {
		return nil, nil, fmt.Errorf("assistant: issue the job's grant: %w", err)
	}
	var claimed store.AssistantPendingAction
	var job store.AssistantBulkJob
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		tq := store.New(tx)
		var err error
		if claimed, err = tq.DecidePendingAction(ctx, store.DecidePendingActionParams{ID: action.ID, UserID: user, Status: "confirmed", Now: now}); err != nil {
			return err
		}
		job, err = tq.StartBulkJob(ctx, store.StartBulkJobParams{
			PendingActionID: action.ID, UserID: user, GrantID: &grantID, GrantExpiresAt: now.Add(bulkGrantTTL),
			ClaimedBy: s.claimedBy(), ClaimedUntil: now.Add(bulkClaimTTL), Now: now,
		})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		revoke, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = s.deps.AssistantGrants.Revoke(revoke, grantID)
		r := refuse(409, codeActionDecided, "This change has already been decided")
		return nil, &r, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("assistant: start the job: %w", err)
	}
	// The action log says who started it and what.
	by := user
	if err := q.InsertAction(context.WithoutCancel(ctx), store.InsertActionParams{
		ID: uuid.New(), RunID: action.RunID, OperationID: action.OperationID, Mode: string(operations.ModeConfirm), ConfirmedBy: &by, Now: now,
	}); err != nil {
		s.deps.Logger.Error("assistant: log a job's start", "run", action.RunID, "error", err)
	}

	queue := newEventQueue()
	queue.emit(eventRun, map[string]any{"runId": action.RunID})
	queue.emit(eventConfirmation, s.cardOf(context.WithoutCancel(ctx), claimed))
	emit := func(rec store.AssistantBulkJob) {
		row, err := q.GetPendingAction(context.Background(), store.GetPendingActionParams{ID: rec.PendingActionID, UserID: rec.UserID})
		if err == nil {
			queue.emit(eventConfirmation, s.cardOf(context.Background(), row))
		}
	}
	s.startBulk(job, emit, func() {
		state := stateAwaiting
		if cur, err := q.GetBulkJobByID(context.Background(), job.ID); err == nil && cur.State != jobAwaiting {
			state = "done"
		}
		if row, err := q.GetPendingAction(context.Background(), store.GetPendingActionParams{ID: job.PendingActionID, UserID: job.UserID}); err == nil {
			queue.emit(eventConfirmation, s.cardOf(context.Background(), row))
		}
		queue.emit(eventDone, map[string]string{"state": state})
		queue.close()
	})
	return &streamResponse{ctx: ctx, queue: queue}, nil, nil
}

// cancelBulk is the person declining a plan that has not started: B's cancel,
// and the job ends with it.
func (s *server) cancelBulk(ctx context.Context, action store.AssistantPendingAction, conv store.GetConversationRow) (*streamResponse, *refusal, error) {
	user := callerID(ctx)
	grantID, err := s.deps.AssistantGrants.Issue(ctx, conv.ID, action.RunID, grantTTL)
	if err != nil {
		return nil, nil, fmt.Errorf("assistant: issue the run's grant: %w", err)
	}
	now := s.deps.Clock()
	var claimed store.AssistantPendingAction
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		tq := store.New(tx)
		var err error
		if claimed, err = tq.DecidePendingAction(ctx, store.DecidePendingActionParams{ID: action.ID, UserID: user, Status: "cancelled", Now: now}); err != nil {
			return err
		}
		if _, err = tq.ResumeRun(ctx, store.ResumeRunParams{ID: action.RunID, UserID: user, Now: now}); err != nil {
			return err
		}
		if job, err := tq.GetBulkJobByAction(ctx, store.GetBulkJobByActionParams{PendingActionID: action.ID, UserID: user}); err == nil {
			code := reasonCancelled
			if _, err := tq.EndBulkJob(ctx, store.EndBulkJobParams{ID: job.ID, State: jobStopped, ErrorCode: &code, Now: now}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			return tq.DeleteBulkItems(ctx, job.ID)
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		revoke, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = s.deps.AssistantGrants.Revoke(revoke, grantID)
		r := refuse(409, codeActionDecided, "This change has already been decided")
		return nil, &r, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("assistant: cancel the plan: %w", err)
	}
	return s.resumeDecided(ctx, conv, claimed, grantID, false)
}

// -- the endpoints ----------------------------------------------------------

// bulkJobOf is the caller's job, and false with the 404 to answer when it is not.
func (s *server) bulkJobOf(ctx context.Context, id uuid.UUID) (store.AssistantBulkJob, *refusal, error) {
	job, err := store.New(s.deps.Pool).GetBulkJob(ctx, store.GetBulkJobParams{ID: id, UserID: callerID(ctx)})
	if errors.Is(err, pgx.ErrNoRows) {
		r := notFound()
		return job, &r, nil
	}
	if err != nil {
		return job, nil, fmt.Errorf("assistant: read the job: %w", err)
	}
	return job, nil, nil
}

// jobView is the job as the contract answers it.
func (s *server) jobView(ctx context.Context, job store.AssistantBulkJob) (gen.AssistantBulkJob, error) {
	q := store.New(s.deps.Pool)
	action, err := q.GetPendingAction(ctx, store.GetPendingActionParams{ID: job.PendingActionID, UserID: job.UserID})
	if err != nil {
		return gen.AssistantBulkJob{}, fmt.Errorf("assistant: read the job's card: %w", err)
	}
	var canary, failures []store.AssistantBulkJobItem
	if job.State != jobPreviewed {
		if canary, err = q.ListBulkCanary(ctx, store.ListBulkCanaryParams{JobID: job.ID, Below: bulkCanarySize}); err != nil {
			return gen.AssistantBulkJob{}, fmt.Errorf("assistant: read the canary: %w", err)
		}
		if failures, err = q.ListBulkFailures(ctx, store.ListBulkFailuresParams{JobID: job.ID, BatchSize: maxBulkFailuresShown}); err != nil {
			return gen.AssistantBulkJob{}, fmt.Errorf("assistant: read the failures: %w", err)
		}
	}
	return bulkJobView(bulkPlanView(action.Preview, job, canary), failures), nil
}

// GetAssistantBulkJob (GET /api/v1/assistant/bulk-jobs/{id})
func (s *server) GetAssistantBulkJob(ctx context.Context, req gen.GetAssistantBulkJobRequestObject) (gen.GetAssistantBulkJobResponseObject, error) {
	if r, err := s.require(ctx); err != nil || r != nil {
		return orRefusal[gen.GetAssistantBulkJobResponseObject](r, err)
	}
	job, r, err := s.bulkJobOf(ctx, req.Id)
	if err != nil || r != nil {
		return orRefusal[gen.GetAssistantBulkJobResponseObject](r, err)
	}
	// The person reading a job whose claim lapsed is how a replica finds it.
	s.resumeLapsed(ctx, job.UserID)
	if job, r, err = s.bulkJobOf(ctx, req.Id); err != nil || r != nil {
		return orRefusal[gen.GetAssistantBulkJobResponseObject](r, err)
	}
	view, err := s.jobView(ctx, job)
	if err != nil {
		return nil, err
	}
	return gen.GetAssistantBulkJob200JSONResponse(view), nil
}

// ContinueAssistantBulkJob (POST /api/v1/assistant/bulk-jobs/{id}/continue)
func (s *server) ContinueAssistantBulkJob(ctx context.Context, req gen.ContinueAssistantBulkJobRequestObject) (gen.ContinueAssistantBulkJobResponseObject, error) {
	if r, err := s.require(ctx); err != nil || r != nil {
		return orRefusal[gen.ContinueAssistantBulkJobResponseObject](r, err)
	}
	ops := s.bulkOps()
	if s.deps.AssistantGrants == nil || ops == nil {
		return refuse(503, codeAIUnavailable, "The assistant's model is not available"), nil
	}
	job, r, err := s.bulkJobOf(ctx, req.Id)
	if err != nil || r != nil {
		return orRefusal[gen.ContinueAssistantBulkJobResponseObject](r, err)
	}
	if job.State != jobAwaiting {
		return refuse(409, codeJobNotWaiting, "This job is not waiting for you to continue"), nil
	}
	if mode, ok := ops.Effective(ctx, job.OperationID); !ok || mode != operations.ModeConfirm {
		return refuse(403, codeNotAllowed, "The assistant may no longer make this change"), nil
	}

	now := s.deps.Clock()
	grantID, err := s.deps.AssistantGrants.Issue(ctx, job.ConversationID, job.RunID, bulkGrantTTL)
	if err != nil {
		return nil, fmt.Errorf("assistant: issue the job's grant: %w", err)
	}
	started, err := store.New(s.deps.Pool).ContinueBulkJob(ctx, store.ContinueBulkJobParams{
		ID: job.ID, UserID: job.UserID, GrantID: &grantID, GrantExpiresAt: now.Add(bulkGrantTTL),
		ClaimedBy: s.claimedBy(), ClaimedUntil: now.Add(bulkClaimTTL), Now: now,
	})
	revoke, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if errors.Is(err, pgx.ErrNoRows) {
		_ = s.deps.AssistantGrants.Revoke(revoke, grantID)
		return refuse(409, codeJobNotWaiting, "This job is not waiting for you to continue"), nil
	}
	if err != nil {
		return nil, fmt.Errorf("assistant: continue the job: %w", err)
	}
	if job.GrantID != nil {
		_ = s.deps.AssistantGrants.Revoke(revoke, *job.GrantID)
	}
	s.startBulk(started, nil, nil)
	view, err := s.jobView(ctx, started)
	if err != nil {
		return nil, err
	}
	return gen.ContinueAssistantBulkJob200JSONResponse(view), nil
}

// stopBulkJob stops a job as the person asks: a plan not started is cancelled
// with its run; a job waiting for Continue ends at once; a running one is told
// to stop and does so between chunks.
func (s *server) stopBulkJob(ctx context.Context, job store.AssistantBulkJob) error {
	q := store.New(s.deps.Pool)
	now := s.deps.Clock()
	switch job.State {
	case jobPreviewed:
		if err := q.CancelRunPendingActions(ctx, store.CancelRunPendingActionsParams{RunID: job.RunID, Now: now}); err != nil {
			return fmt.Errorf("assistant: cancel the plan: %w", err)
		}
		code := reasonCancelled
		if _, err := q.EndBulkJob(ctx, store.EndBulkJobParams{ID: job.ID, State: jobStopped, ErrorCode: &code, Now: now}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("assistant: end the plan: %w", err)
		}
		if err := q.DeleteBulkItems(ctx, job.ID); err != nil {
			return fmt.Errorf("assistant: drop the plan's items: %w", err)
		}
		if _, err := q.FinishRun(ctx, store.FinishRunParams{ID: job.RunID, State: "stopped", Now: now}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("assistant: end the run: %w", err)
		}
	case jobAwaiting:
		s.endBulk(ctx, job, jobStopped, reasonByPerson)
	case jobCanary, jobRunning, jobStopping:
		if _, err := q.RequestBulkStop(ctx, store.RequestBulkStopParams{ID: job.ID, UserID: job.UserID, Now: now}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("assistant: stop the job: %w", err)
		}
	}
	return nil
}

// StopAssistantBulkJob (POST /api/v1/assistant/bulk-jobs/{id}/stop)
func (s *server) StopAssistantBulkJob(ctx context.Context, req gen.StopAssistantBulkJobRequestObject) (gen.StopAssistantBulkJobResponseObject, error) {
	if r, err := s.require(ctx); err != nil || r != nil {
		return orRefusal[gen.StopAssistantBulkJobResponseObject](r, err)
	}
	job, r, err := s.bulkJobOf(ctx, req.Id)
	if err != nil || r != nil {
		return orRefusal[gen.StopAssistantBulkJobResponseObject](r, err)
	}
	if err := s.stopBulkJob(ctx, job); err != nil {
		return nil, err
	}
	if job, r, err = s.bulkJobOf(ctx, req.Id); err != nil || r != nil {
		return orRefusal[gen.StopAssistantBulkJobResponseObject](r, err)
	}
	view, err := s.jobView(ctx, job)
	if err != nil {
		return nil, err
	}
	return gen.StopAssistantBulkJob200JSONResponse(view), nil
}

// failedItems are a job's failed items in order, for the CSV.
func (s *server) failedItems(ctx context.Context, job store.AssistantBulkJob) ([]store.AssistantBulkJobItem, error) {
	rows, err := store.New(s.deps.Pool).ListBulkFailures(ctx, store.ListBulkFailuresParams{JobID: job.ID, BatchSize: maxBulkItems})
	if err != nil {
		return nil, fmt.Errorf("assistant: read the failed items: %w", err)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Seq < rows[j].Seq })
	return rows, nil
}
