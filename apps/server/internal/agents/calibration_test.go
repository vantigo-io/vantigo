package agents_test

import (
	"math"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents"
)

// Calibration (virtual employees design V21) from fixed labels.

func repeat(n int, l agents.Label) []agents.Label {
	out := make([]agents.Label, n)
	for i := range out {
		out[i] = l
	}
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCalibrate_FromFixedLabels(t *testing.T) {
	t.Parallel()
	var labels []agents.Label
	labels = append(labels, repeat(20, agents.Label{Score: 95})...)             // good, high
	labels = append(labels, repeat(10, agents.Label{Score: 95, Bad: true})...)  // bad, high
	labels = append(labels, repeat(10, agents.Label{Score: 70})...)             // good, middling
	labels = append(labels, repeat(10, agents.Label{Score: 70, Bad: true})...)  // bad, middling
	labels = append(labels, repeat(10, agents.Label{Score: 99, Held: true})...) // held, whatever the score
	c := agents.Calibrate(labels, 85)

	if c.NotEnoughHistory || c.Labelled != 60 || c.HeldCount != 10 || c.Threshold != 85 || c.Minimum != 50 {
		t.Fatalf("calibration = %+v", c)
	}
	if len(c.Points) != 51 || c.Points[0].X != 50 || c.Points[50].X != 100 {
		t.Fatalf("points = %d from %d to %d, want 51 from 50 to 100", len(c.Points), c.Points[0].X, c.Points[len(c.Points)-1].X)
	}
	at := func(x int) (auto, bad int32, autoShare, badShare float64) {
		p := c.Points[x-50]
		return p.AutoCount, p.BadCount, p.AutoShare, p.BadShare
	}
	// At 50 everything not held would run by itself: 50 of 60, of which 20 went wrong.
	if auto, bad, as, bs := at(50); auto != 50 || bad != 20 || !near(as, 50.0/60) || !near(bs, 0.4) {
		t.Errorf("X=50: %d auto, %d bad, shares %v and %v", auto, bad, as, bs)
	}
	// At 71 only the 95s remain: 30 of 60, of which 10 went wrong.
	if auto, bad, as, bs := at(71); auto != 30 || bad != 10 || !near(as, 0.5) || !near(bs, 1.0/3) {
		t.Errorf("X=71: %d auto, %d bad, shares %v and %v", auto, bad, as, bs)
	}
	// The 70s fall out exactly above 70.
	if auto, _, _, _ := at(70); auto != 50 {
		t.Errorf("X=70 keeps %d, want the 70s still in", auto)
	}
	// The held 99s never run by itself, so nothing is left above 95.
	if auto, bad, as, bs := at(96); auto != 0 || bad != 0 || as != 0 || bs != 0 {
		t.Errorf("X=96: %d auto, %d bad, shares %v and %v, want none", auto, bad, as, bs)
	}
}

func TestCalibrate_FewerThanFiftyLabelsIsNotEnoughHistory(t *testing.T) {
	t.Parallel()
	c := agents.Calibrate(repeat(49, agents.Label{Score: 90}), 85)
	if !c.NotEnoughHistory || c.Labelled != 49 || len(c.Points) != 0 {
		t.Errorf("49 labels = %+v, want not enough history with the count and no points", c)
	}
	if c := agents.Calibrate(repeat(50, agents.Label{Score: 90}), 85); c.NotEnoughHistory || len(c.Points) != 51 {
		t.Errorf("50 labels = %+v, want figures", c)
	}
}

func TestEditRatio_IsCharacterLevel(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		before, after string
		want          float64
	}{
		{"", "", 0},
		{"same", "same", 0},
		{"abcd", "abce", 0.25},
		{"abc", "", 1},
		{"hello world", "hello brave world", 6.0 / 17},
		{"æøå", "æøa", 1.0 / 3},
	} {
		if got := agents.EditRatio(c.before, c.after); !near(got, c.want) {
			t.Errorf("EditRatio(%q, %q) = %v, want %v", c.before, c.after, got, c.want)
		}
	}
}

func TestBlocking_OnlyWhatTheReviewerWasNotShownStopsAnApproval(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name       string
		held, prev []string
		edited     bool
		want       []string
	}{
		{"nothing fails", nil, nil, false, nil},
		{"S1 the reviewer was shown", []string{"S1"}, []string{"S1"}, false, nil},
		{"S1 that is new", []string{"S1"}, nil, false, []string{"S1"}},
		{"S2 that is new", []string{"S2"}, []string{"S1"}, true, []string{"S2"}},
		{"C1 always", []string{"C1"}, []string{"C1"}, false, []string{"C1"}},
		{"the soft rules never", []string{"S3", "S4", "S5", "C2"}, nil, true, nil},
		{"a check that failed before and text unedited", []string{"CHECK"}, []string{"CHECK"}, false, nil},
		{"a check that fails on the reviewer's text", []string{"CHECK"}, []string{"CHECK"}, true, []string{"CHECK"}},
	} {
		got := agents.Blocking(c.held, c.prev, c.edited)
		if len(got) != len(c.want) || (len(got) > 0 && got[0] != c.want[0]) {
			t.Errorf("%s: blocking = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCalibration_TheScreenIsComputedOnTheServerFromDecidedReplies(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	view := g.h.SignIn(t, "agents:view")

	type calibration struct {
		Labelled         int  `json:"labelled"`
		NotEnoughHistory bool `json:"notEnoughHistory"`
		HeldCount        int  `json:"heldCount"`
		Threshold        int  `json:"threshold"`
		Points           []struct {
			X         int     `json:"x"`
			AutoCount int     `json:"autoCount"`
			BadCount  int     `json:"badCount"`
			BadShare  float64 `json:"badShare"`
		} `json:"points"`
	}
	get := func() calibration {
		r := view.Do(http.MethodGet, "/api/v1/agents/roles/"+g.role.ID+"/calibration", nil)
		if r.Status != http.StatusOK {
			t.Fatalf("calibration: %d %s", r.Status, r.Body)
		}
		var c calibration
		r.JSON(&c)
		return c
	}

	for range 3 {
		g.seed(t, "reply", map[string]any{}, func(o *seedOpts) { o.State = "sent" })
	}
	if c := get(); !c.NotEnoughHistory || c.Labelled != 3 || len(c.Points) != 0 {
		t.Fatalf("three labels = %+v, want not enough history with the count", c)
	}

	// Sixty more: edits at the 20 % line are not bad, beyond it they are; a rule
	// that is only the machinery's (SEND) is not a hold; a waiting item and a
	// withdrawn one are no labels.
	ratio := func(v float64) *float64 { return &v }
	for range 20 {
		g.seed(t, "reply", map[string]any{}, func(o *seedOpts) { o.State = "sent"; o.Score = 92 })
	}
	for range 10 {
		g.seed(t, "reply", map[string]any{}, func(o *seedOpts) { o.State = "edited_sent"; o.Score = 92; o.EditRatio = ratio(0.20) })
	}
	for range 10 {
		g.seed(t, "reply", map[string]any{}, func(o *seedOpts) { o.State = "edited_sent"; o.Score = 92; o.EditRatio = ratio(0.21) })
	}
	for range 10 {
		g.seed(t, "reply", map[string]any{}, func(o *seedOpts) { o.State = "discarded"; o.Score = 60 })
	}
	for range 10 {
		g.seed(t, "reply", map[string]any{}, func(o *seedOpts) { o.State = "sent"; o.Score = 99; o.HeldBy = []string{"S4"} })
	}
	for range 5 {
		g.seed(t, "reply", map[string]any{}, func(o *seedOpts) { o.State = "sent"; o.Score = 80; o.HeldBy = []string{"SEND"} })
	}
	g.seed(t, "reply", map[string]any{}, nil)                                                       // waiting
	g.seed(t, "reply", map[string]any{}, func(o *seedOpts) { o.State = "withdrawn" })               // no label
	g.seed(t, "change", map[string]any{"operationId": "x"}, func(o *seedOpts) { o.State = "sent" }) // a change is no label

	c := get()
	if c.NotEnoughHistory || c.Labelled != 68 || c.HeldCount != 10 || c.Threshold != 85 {
		t.Fatalf("calibration = %+v, want 68 labelled and 10 held", c)
	}
	p := c.Points[85-50]
	// At 85: the 3 sent first, the 20 sent and the 20 edited would run by itself (43),
	// of which the 10 edited beyond 20 % went wrong; the SEND-held ones at 80 do not reach it.
	if p.X != 85 || p.AutoCount != 43 || p.BadCount != 10 || !near(p.BadShare, 10.0/43) {
		t.Errorf("X=85 = %+v, want 43 automatic and 10 bad", p)
	}
	if r := view.Do(http.MethodGet, "/api/v1/agents/roles/"+uuid.NewString()+"/calibration", nil); r.Status != http.StatusNotFound {
		t.Errorf("an unknown role = %d, want 404", r.Status)
	}
	if r := g.h.SignIn(t).Do(http.MethodGet, "/api/v1/agents/roles/"+g.role.ID+"/calibration", nil); r.Status != http.StatusForbidden {
		t.Errorf("calibration without agents:view = %d, want 403", r.Status)
	}
}
