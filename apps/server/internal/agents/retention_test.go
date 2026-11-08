package agents_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Retention (virtual employees design V26): runs and what hangs on them are kept
// for the retention in months, never longer than Communications keeps its
// messages; the day's usage for 24 months.

// oldRun inserts an ended run with a transcript, an action and a review item.
func (g *queueRig) oldRun(t *testing.T, age time.Duration) uuid.UUID {
	t.Helper()
	at := g.h.Now().Add(-age)
	conv := g.comms.conversation(g.channel, at)
	id := g.seedOn(t, conv, "reply", map[string]any{}, func(o *seedOpts) { o.State = "sent"; o.CreatedAt = at; o.Expires = at.Add(24 * time.Hour) })
	run, _ := uuid.Parse(runOf(t, g, id))
	g.h.Exec(t, `INSERT INTO agents.run_messages (run_id, role, text, created_at) VALUES ($1, 'user', 'x', $2)`, run, at)
	g.h.Exec(t, `INSERT INTO agents.actions (run_id, agent_id, operation_id, ref, created_at) VALUES ($1, $2, 'getInvoices', 'getInvoices', $3)`, run, g.agent.ID, at)
	g.h.Exec(t, `INSERT INTO agents.handed_off (conversation_id, role_id, at, reason) VALUES ($1, $2, $3, 'hand_off')`, conv.ID, g.role.ID, at)
	return run
}

func (g *queueRig) runExists(t *testing.T, id uuid.UUID) bool {
	return g.rows(t, `SELECT count(*) FROM agents.runs WHERE id = $1`, id) == 1
}

func TestRetention_DeletesRunsAndWhatHangsOnThemOlderThanTheSetting(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	day := 24 * time.Hour
	old := g.oldRun(t, 200*day) // past six months
	recent := g.oldRun(t, 100*day)
	if err := agents.RetentionCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if g.runExists(t, old) || !g.runExists(t, recent) {
		t.Fatalf("old run kept=%v, recent run kept=%v, want the old one deleted and the recent one kept", g.runExists(t, old), g.runExists(t, recent))
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.run_messages WHERE run_id = $1`, old); n != 0 {
		t.Errorf("the old transcript is kept: %d", n)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.actions WHERE run_id = $1`, old); n != 0 {
		t.Errorf("the old action log is kept: %d", n)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.review_items WHERE run_id = $1`, old); n != 0 {
		t.Errorf("the old review item is kept: %d", n)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.review_items WHERE run_id = $1`, recent); n != 1 {
		t.Errorf("the recent review item went with the old: %d", n)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.handed_off`); n != 1 {
		t.Errorf("hand-off markers = %d, want the recent one", n)
	}
}

func TestRetention_UsageIsKeptTwentyFourMonths(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	now := g.h.Now().UTC()
	g.h.Exec(t, `INSERT INTO agents.usage_days (agent_id, day, runs) VALUES ($1, $2, 1), ($1, $3, 1), ($1, $4, 1)`,
		g.agent.ID, now.AddDate(0, -25, 0).Format(time.DateOnly), now.AddDate(0, -23, 0).Format(time.DateOnly), now.Format(time.DateOnly))
	if err := agents.RetentionCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.usage_days`); n != 2 {
		t.Errorf("usage days = %d, want the 25-month-old one deleted", n)
	}
}

func TestRetention_IsCappedAtCommunicationsOwnAndLoweredNightly(t *testing.T) {
	t.Parallel()
	// Communications keeps its messages 95 days: three 30-day months.
	h := newHarness(t, modtest.WithEnv("COMMUNICATIONS_RETENTION_DAYS", "95"))
	if got := agents.RetentionCapMonths(h.Deps()); got != 3 {
		t.Fatalf("the cap = %d months, want 3", got)
	}
	o := owner(t, h)
	s := getSettings(t, o)
	var saved struct {
		RetentionMonths    int `json:"retentionMonths"`
		RetentionCapMonths int `json:"retentionCapMonths"`
		Version            int `json:"version"`
	}
	r := o.Do(http.MethodPut, "/api/v1/agents/settings", map[string]any{"companyName": "Hansen Rør", "retentionMonths": 12}, ifMatch(s.Version))
	if r.Status != http.StatusOK {
		t.Fatalf("save a retention of 12 months: %d %s", r.Status, r.Body)
	}
	r.JSON(&saved)
	if saved.RetentionMonths != 3 || saved.RetentionCapMonths != 3 {
		t.Errorf("saved = %+v, want the retention lowered to Communications' 3", saved)
	}

	// A retention that outlives Communications' (the setting was kept when the
	// installation shortened its own) is lowered by the nightly pass, and audited.
	h.Exec(t, `UPDATE agents.settings SET retention_months = 12`)
	if err := agents.RetentionCycle(h.Deps()); err != nil {
		t.Fatal(err)
	}
	if got := getSettings(t, o); got.RetentionMonths != 3 {
		t.Errorf("after the pass the retention is %d months, want 3", got.RetentionMonths)
	}
	if n := h.Count(t, `SELECT count(*) FROM agents.events WHERE action = 'settings.retention_lowered'`); n != 1 {
		t.Errorf("lowering events = %d, want 1", n)
	}
}

func TestRetention_RunsPastCommunicationsOwnRetentionAreDeletedWhateverTheSetting(t *testing.T) {
	t.Parallel()
	h, comms, check := newHarnessWith(t, modtest.WithEnv("COMMUNICATIONS_RETENTION_DAYS", "95"))
	o := owner(t, h)
	setCompany(t, o, "Hansen Rør")
	a := createAgent(t, o, "Hanna")
	r := addRole(t, o, a.ID, "Front desk")
	g := &queueRig{rig: &rig{h: h, owner: o, agent: a, role: r, channel: uuid.New()}, comms: comms, check: check}
	// The setting is six months; the messages go after 95 days.
	h.Exec(t, `UPDATE agents.settings SET retention_months = 3`)
	day := 24 * time.Hour
	old := g.oldRun(t, 100*day)
	recent := g.oldRun(t, 80*day)
	if err := agents.RetentionCycle(h.Deps()); err != nil {
		t.Fatal(err)
	}
	if g.runExists(t, old) || !g.runExists(t, recent) {
		t.Errorf("a run 100 days old kept=%v, one 80 days old kept=%v", g.runExists(t, old), g.runExists(t, recent))
	}
}
