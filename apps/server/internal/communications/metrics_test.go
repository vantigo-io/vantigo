package communications_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is task 14's metrics: SV/CommunicationsMetrics.cs, inventory §18.
// Four unlabelled counters on one meter named Vantigo.Communications, all of
// them the outbox's, and — the part that needs a test rather than a comment —
// nothing else.
//
// Every worker under test gets its OWN MeterProvider with its own manual
// reader (export_test.go's NewOutboxWorkerWithMeterForTest). A package-level
// instrument on the global provider would make each of these assertions a
// race against every other parallel test in this package that happens to run
// a worker, which is exactly the kind of test that passes until it matters.

const (
	possibleDuplicateSendsMetric = "communications.outbox.possible_duplicate_sends"
	jobsCompletedMetric          = "communications.outbox.jobs_completed"
	jobsRetriedMetric            = "communications.outbox.jobs_retried"
	jobsFailedMetric             = "communications.outbox.jobs_failed"
)

// meterHarness is one MeterProvider and the reader that can be asked what it
// has collected.
type meterHarness struct {
	provider *sdkmetric.MeterProvider
	reader   sdkmetric.Reader
}

func newMeterHarness(t *testing.T) *meterHarness {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	return &meterHarness{provider: provider, reader: reader}
}

// worker builds an outbox worker that reports to this harness's meter.
func (m *meterHarness) worker(t *testing.T, h *modtest.Harness) *communications.OutboxWorker {
	t.Helper()
	return communications.NewOutboxWorkerWithMeterForTest(h.Deps(), m.provider)
}

// scope returns the Vantigo.Communications scope's collected metrics, or an
// empty slice when the meter has produced nothing yet.
func (m *meterHarness) scope(t *testing.T) []metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := m.reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name == "Vantigo.Communications" {
			return sm.Metrics
		}
	}
	return nil
}

// count is the total of an int64 counter, 0 when it has never been
// incremented. It also enforces inventory §18's "unlabelled": a data point
// carrying attributes is a different time series under the same name, which
// is worse for an operator than a differently named metric.
func (m *meterHarness) count(t *testing.T, name string) int64 {
	t.Helper()
	for _, md := range m.scope(t) {
		if md.Name != name {
			continue
		}
		sum, ok := md.Data.(metricdata.Sum[int64])
		if !ok {
			t.Fatalf("metric %s is %T, want metricdata.Sum[int64] (an int64 counter)", name, md.Data)
		}
		var total int64
		for _, dp := range sum.DataPoints {
			if dp.Attributes.Len() != 0 {
				t.Errorf("metric %s carries attributes, want none: .NET's counters take no tags (inventory §18)", name)
			}
			total += dp.Value
		}
		return total
	}
	return 0
}

// TestOutboxMetrics_TheMeterCarriesExactlyTheFourNamedCounters is the
// "invent nothing" gate, and it is the reason this test exists at all rather
// than a comment saying not to add more.
//
// Inventory `:1880` is binding: .NET has NO metrics for retention,
// attachment cleanup, object storage, SMTP latency or the AI feature, and no
// histograms or gauges anywhere in the module. Each of those is an obvious,
// well-meant addition, and each would put a metric on an operator's
// dashboard that the module being replaced never emitted. A fifth instrument
// under this meter fails here, which makes adding one a deliberate act with
// this test and the inventory to update rather than a quiet extra line.
func TestOutboxMetrics_TheMeterCarriesExactlyTheFourNamedCounters(t *testing.T) {
	t.Parallel()
	m := newMeterHarness(t)

	// Every counter has to be incremented at least once for this assertion
	// to mean anything: the SDK exports an instrument only once it has
	// recorded a measurement, so a set collected after a single clean send
	// would be {jobs_completed} and would happily match a meter that had
	// twelve other instruments on it. Two harnesses share one MeterProvider
	// — the provider is independent of the database — so one job can take
	// the crash-recovery path and another the failure path without the two
	// racing to be claimed out of the same queue.
	crashed := &fakeSMTP{}
	hc := newOutboxHarness(t, crashed)
	fx := seedOutboxJob(t, hc)
	// A crashed worker's leftovers: processing, lease expired, marker set —
	// which counts a possible duplicate at claim — with nothing left to
	// send, which completes the job without sending.
	hc.Exec(t, `UPDATE communications.outbox_jobs
	            SET status = 'processing', lease_id = $2, lease_until = $3, delivery_attempted_at = $4
	            WHERE message_id = $1`,
		fx.messageID, "0123456789abcdef0123456789abcdef", hc.Now().Add(-time.Minute), hc.Now().Add(-2*time.Minute))
	hc.Exec(t, `UPDATE communications.message_deliveries SET status = 'relay_accepted' WHERE message_id = $1`, fx.messageID)
	if _, err := m.worker(t, hc).ProcessOne(context.Background()); err != nil {
		t.Fatalf("crash-recovery ProcessOne: %v", err)
	}

	failing := &fakeSMTP{}
	failing.failWith(fmt.Errorf("smtp is down"))
	hf := newOutboxHarness(t, failing)
	seedOutboxJob(t, hf)
	wf := m.worker(t, hf)
	// Seven retries and then the terminal eighth, so jobs_retried and
	// jobs_failed have both been touched.
	for _, backoff := range []time.Duration{2, 4, 8, 16, 32, 64, 128} {
		if _, err := wf.ProcessOne(context.Background()); err != nil {
			t.Fatalf("failing ProcessOne: %v", err)
		}
		hf.Advance(backoff * time.Second)
	}
	if _, err := wf.ProcessOne(context.Background()); err != nil {
		t.Fatalf("terminal ProcessOne: %v", err)
	}

	for name, want := range map[string]int64{
		possibleDuplicateSendsMetric: 1,
		jobsCompletedMetric:          1,
		jobsRetriedMetric:            7,
		jobsFailedMetric:             1,
	} {
		if got := m.count(t, name); got != want {
			t.Errorf("%s = %d, want %d: every counter must be exercised for the set assertion below to mean anything",
				name, got, want)
		}
	}

	var names []string
	for _, md := range m.scope(t) {
		names = append(names, md.Name)
	}
	slices.Sort(names)
	want := []string{jobsCompletedMetric, jobsFailedMetric, jobsRetriedMetric, possibleDuplicateSendsMetric}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Errorf("meter Vantigo.Communications exports %v, want exactly %v", names, want)
	}
}

// TestOutboxMetrics_SuccessfulSendCountsOneCompletion pins the first of the
// three job-outcome counters against a real send through the real worker.
func TestOutboxMetrics_SuccessfulSendCountsOneCompletion(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	h := newOutboxHarness(t, f)
	fx := seedOutboxJob(t, h)
	m := newMeterHarness(t)

	if _, err := m.worker(t, h).ProcessOne(context.Background()); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if got := readOutboxJob(t, h, fx.messageID).status; got != "completed" {
		t.Fatalf("job status = %q, want completed", got)
	}

	if got := m.count(t, jobsCompletedMetric); got != 1 {
		t.Errorf("jobs_completed = %d, want 1", got)
	}
	for _, name := range []string{jobsRetriedMetric, jobsFailedMetric, possibleDuplicateSendsMetric} {
		if got := m.count(t, name); got != 0 {
			t.Errorf("%s = %d, want 0 on a clean send", name, got)
		}
	}
}

// TestOutboxMetrics_NothingSendableAlsoCountsACompletion is inventory §18's
// easily missed half: CompleteWithoutSendingAsync increments jobs_completed
// too (`OutboxJobProcessor.cs:213`). A job with nothing left to send is a
// completed job, not an ignored one, and a port that counted only the
// post-send completion would quietly under-report.
func TestOutboxMetrics_NothingSendableAlsoCountsACompletion(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	h := newOutboxHarness(t, f)
	fx := seedOutboxJob(t, h)
	// relay_accepted is one of IsSendable's three excluded statuses, so the
	// claim finds the job and the send phase finds nothing to send.
	h.Exec(t, `UPDATE communications.message_deliveries SET status = 'relay_accepted' WHERE message_id = $1`, fx.messageID)
	m := newMeterHarness(t)

	if _, err := m.worker(t, h).ProcessOne(context.Background()); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if len(f.sends()) != 0 {
		t.Fatalf("sends = %d, want 0", len(f.sends()))
	}
	if got := m.count(t, jobsCompletedMetric); got != 1 {
		t.Errorf("jobs_completed = %d, want 1: CompleteWithoutSending counts too", got)
	}
}

// TestOutboxMetrics_SuppressedCancellationCountsNothing is the other side of
// that rule, and the one that is easy to get wrong in the opposite
// direction: a job cancelled because a recipient is suppressed reaches a
// terminal status too, but .NET increments NO counter for it (inventory §18:
// "Not incremented for cancelled"). It is neither a completion nor a
// failure, and counting it as either would corrupt both series.
func TestOutboxMetrics_SuppressedCancellationCountsNothing(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	h := newOutboxHarness(t, f)
	fx := seedOutboxJob(t, h)
	admin := h.SignIn(t, "communications:suppressions-manage")
	createSuppression(t, admin, map[string]any{"emailAddress": fx.recipients[0]}, 201)
	m := newMeterHarness(t)

	if _, err := m.worker(t, h).ProcessOne(context.Background()); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if got := readOutboxJob(t, h, fx.messageID).status; got != "cancelled" {
		t.Fatalf("job status = %q, want cancelled", got)
	}
	if len(f.sends()) != 0 {
		t.Fatalf("sends = %d, want 0: a suppressed recipient cancels the whole job", len(f.sends()))
	}

	for _, name := range []string{jobsCompletedMetric, jobsRetriedMetric, jobsFailedMetric} {
		if got := m.count(t, name); got != 0 {
			t.Errorf("%s = %d, want 0: a cancelled job is neither completed nor failed", name, got)
		}
	}
}

// TestOutboxMetrics_RetriesThenTerminalFailureSplitTheTwoFailureCounters
// walks one job all the way to terminality: seven non-terminal failures on
// jobs_retried and the eighth — attempts >= max_attempts, post-increment —
// on jobs_failed, never both. jobs_failed is the counter .NET's own doc
// comment says to alert on ("Every increment is undelivered customer
// email"), so a retry leaking into it would page someone for a job that is
// still going to be delivered.
func TestOutboxMetrics_RetriesThenTerminalFailureSplitTheTwoFailureCounters(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	f.failWith(fmt.Errorf("smtp is down"))
	h := newOutboxHarness(t, f)
	fx := seedOutboxJob(t, h)
	m := newMeterHarness(t)
	w := m.worker(t, h)

	for i, backoff := range []time.Duration{2, 4, 8, 16, 32, 64, 128} {
		if _, err := w.ProcessOne(context.Background()); err != nil {
			t.Fatalf("attempt %d: ProcessOne: %v", i+1, err)
		}
		if got := m.count(t, jobsRetriedMetric); got != int64(i+1) {
			t.Fatalf("after attempt %d: jobs_retried = %d, want %d", i+1, got, i+1)
		}
		if got := m.count(t, jobsFailedMetric); got != 0 {
			t.Fatalf("after attempt %d: jobs_failed = %d, want 0 while the job is still retryable", i+1, got)
		}
		h.Advance(backoff * time.Second)
	}

	if _, err := w.ProcessOne(context.Background()); err != nil {
		t.Fatalf("terminal attempt: ProcessOne: %v", err)
	}
	if got := readOutboxJob(t, h, fx.messageID).status; got != "failed" {
		t.Fatalf("job status = %q, want failed", got)
	}
	if got := m.count(t, jobsFailedMetric); got != 1 {
		t.Errorf("jobs_failed = %d, want exactly 1", got)
	}
	if got := m.count(t, jobsRetriedMetric); got != 7 {
		t.Errorf("jobs_retried = %d, want 7: the terminal attempt is not a retry", got)
	}
	if got := m.count(t, jobsCompletedMetric); got != 0 {
		t.Errorf("jobs_completed = %d, want 0", got)
	}
}
