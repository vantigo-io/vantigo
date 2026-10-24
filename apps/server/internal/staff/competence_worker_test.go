package staff_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff"
)

// The staff-competence worker (staff competence design S7, §4, §9; plan Task
// 3b, reading 4): a current row of an active, unerased person is stamped
// expiring within its competence's lead of its end, and expired once the end
// has passed, each once, before the person and their manager-deciders are
// mailed — outside every transaction, in each one's language, never failing
// the cycle and never logging an address.

// catalogueID is the id of the competence keyed key.
func catalogueID(t *testing.T, h *harness, key string) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `SELECT id FROM staff.competences WHERE key = $1`, key)
}

// companySkill adds a company skill with a level under the trade category,
// as HR's catalogue does, and answers its id.
func companySkill(t *testing.T, h *harness, key, name string) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `
		INSERT INTO staff.competences (category_id, key, name_en, name_nb, kind, has_level, sort, created_at, updated_at)
		VALUES ((SELECT id FROM staff.competence_categories WHERE key = 'fag'), $1, $2, $2, 'skill', true, 100, $3, $3)
		RETURNING id`, key, name, h.Now())
}

// competenceRow is one person's competence written directly: the person's
// endpoints are Task 3a's, and the worker, the items and the provider read
// the table alone.
type competenceRow struct {
	person     int64
	competence int64
	level      *int16
	from       string
	to         *string
	verified   bool
}

// rawPersonCompetence writes r as a current row and answers its id.
func rawPersonCompetence(t *testing.T, h *harness, r competenceRow) int64 {
	t.Helper()
	var verifiedAt *time.Time
	source := "self"
	if r.verified {
		now := h.Now()
		verifiedAt, source = &now, "manager"
	}
	return modtest.One[int64](t, h.Harness, `
		INSERT INTO staff.person_competences (person_id, competence_id, level, valid_from, valid_to, source, verified_at,
			verified_basis, created_at, updated_at)
		VALUES ($1, $2, $3, $4::date, $5::date, $6, $7, CASE WHEN $7::timestamptz IS NULL THEN NULL ELSE 'evidence' END, $8, $8)
		RETURNING id`, r.person, r.competence, r.level, r.from, r.to, source, verifiedAt, h.Now())
}

// renewRow writes the renewal of row old valid to to, superseding old as the
// renewal does, and answers the new row's id.
func renewRow(t *testing.T, h *harness, old int64, to string) int64 {
	t.Helper()
	// The current row is unique per person and competence: old points at
	// itself until the new row exists, then at the new row.
	h.Exec(t, `UPDATE staff.person_competences SET superseded_by_id = id WHERE id = $1`, old)
	renewed := modtest.One[int64](t, h.Harness, `
		INSERT INTO staff.person_competences (person_id, competence_id, valid_from, valid_to, source, verified_at,
			verified_basis, created_at, updated_at)
		SELECT person_id, competence_id, $2::date, $3::date, 'manager', $4, 'evidence', $4, $4
		FROM staff.person_competences WHERE id = $1 RETURNING id`, old, h.Now().Format(time.DateOnly), to, h.Now())
	h.Exec(t, `UPDATE staff.person_competences SET superseded_by_id = $2 WHERE id = $1`, old, renewed)
	return renewed
}

// stamps is row's two notified stamps, set or not.
func stamps(t *testing.T, h *harness, row int64) (expiring, expired bool) {
	t.Helper()
	return modtest.One[bool](t, h.Harness, `SELECT expiring_notified_at IS NOT NULL FROM staff.person_competences WHERE id = $1`, row),
		modtest.One[bool](t, h.Harness, `SELECT expired_notified_at IS NOT NULL FROM staff.person_competences WHERE id = $1`, row)
}

// competenceWorker is the staff-competence worker of h's composition.
func competenceWorker(t *testing.T, h *harness) *staff.CompetenceWorker {
	t.Helper()
	for _, w := range h.Workers(h.Deps()) {
		if c, ok := w.(*staff.CompetenceWorker); ok {
			return c
		}
	}
	t.Fatal("no staff-competence worker")
	return nil
}

// runCompetence runs one cycle and fails t on its error.
func runCompetence(t *testing.T, h *harness) {
	t.Helper()
	if err := competenceWorker(t, h).RunCycle(context.Background()); err != nil {
		t.Fatalf("competence cycle: %v", err)
	}
}

// competenceWorld is Kari, managed by Bjørn — whose link names an active
// user, so he is her manager-decider — each with an address: Kari's private
// one, in Norwegian, Bjørn's work one, in English. The clock is the
// harness's start, Saturday 12 September 2026.
type competenceWorld struct {
	h          *harness
	kari, boss int64
	bossC      *modtest.Client
	bossUser   uuid.UUID
}

const (
	kariAddress = "kari@example.test"
	bossAddress = "bjorn@example.test"
)

func newCompetenceWorld(t *testing.T, opts ...modtest.Option) competenceWorld {
	t.Helper()
	h := newHarness(t, opts...)
	bossC, bossUser := signIn(t, h)
	boss := seedPerson(t, h, "Bjørn", "Sjef")
	link(t, h, boss, bossUser)
	seedEmployment(t, h, boss, 0)
	kari := seedPerson(t, h, "Kari", "Nordmann")
	seedEmployment(t, h, kari, boss)
	h.Exec(t, `UPDATE staff.people SET work_email = $2, preferred_language = 'en' WHERE id = $1`, boss, bossAddress)
	h.Exec(t, `UPDATE staff.people SET private_email = $2, preferred_language = 'nb' WHERE id = $1`, kari, kariAddress)
	return competenceWorld{h: h, kari: kari, boss: boss, bossC: bossC, bossUser: bossUser}
}

func strPtr(s string) *string { return &s }

// Expiring and expired, each mailed once to the person and the manager, the
// stamps set before the send; a second cycle sends nothing; a row whose end
// is beyond its lead, and a competence with no lead, raise nothing.
func TestCompetenceWorker_MailsOnceAndStamps(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorld(t)
	h := w.h
	// HMS-kort's lead is 60 days: 12 October is within it.
	hms := rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: catalogueID(t, h, "hms-kort"), from: "2024-10-13", to: strPtr("2026-10-12"), verified: true})
	// FSE ended on 1 September.
	fse := rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: catalogueID(t, h, "fse"), from: "2025-09-02", to: strPtr("2026-09-01"), verified: true})
	// Førerkort B's lead is 180 days: a year ahead is beyond it.
	far := rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: catalogueID(t, h, "forerkort-b"), from: "2012-09-01", to: strPtr("2027-09-12"), verified: true})
	// Truck has no lead: it never fires before its end.
	truck := rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: catalogueID(t, h, "truck"), from: "2020-01-01", to: strPtr("2026-09-13"), verified: true})

	runCompetence(t, h)
	if e, x := stamps(t, h, hms); !e || x {
		t.Errorf("HMS-kort stamps = expiring %v, expired %v; want expiring alone", e, x)
	}
	if e, x := stamps(t, h, fse); e || !x {
		t.Errorf("FSE stamps = expiring %v, expired %v; want expired alone", e, x)
	}
	for _, row := range []int64{far, truck} {
		if e, x := stamps(t, h, row); e || x {
			t.Errorf("row %d stamped: expiring %v, expired %v", row, e, x)
		}
	}

	toKari, toBoss := mailsTo(h, kariAddress), mailsTo(h, bossAddress)
	if len(toKari) != 2 || len(toBoss) != 2 || len(h.mail.messages()) != 4 {
		t.Fatalf("mails: %d to Kari, %d to Bjørn, %d in all; want two each", len(toKari), len(toBoss), len(h.mail.messages()))
	}
	joinedKari, joinedBoss := strings.Join(toKari, "\n---\n"), strings.Join(toBoss, "\n---\n")
	for _, want := range []string{"Hei Kari Nordmann", "«HMS-kort» utløper snart", "2026-10-12", "«FSE-opplæring med førstehjelp» er utløpt", "2026-09-01", "/staff/me"} {
		if !strings.Contains(joinedKari, want) {
			t.Errorf("Kari's mails lack %q:\n%s", want, joinedKari)
		}
	}
	for _, want := range []string{"Hi Bjørn Sjef", "“HMS card” of Kari Nordmann expires soon", "2026-10-12", "“FSE training with first aid” of Kari Nordmann has expired",
		"/staff/people/" + idText(w.kari) + "?tab=competence"} {
		if !strings.Contains(joinedBoss, want) {
			t.Errorf("Bjørn's mails lack %q:\n%s", want, joinedBoss)
		}
	}
	leaksIn(t, "the mails", joinedKari+joinedBoss, "Førerkort", "Driving licence", "Truck", "Forklift")

	// Idempotent through the stamps.
	runCompetence(t, h)
	if n := len(h.mail.messages()); n != 4 {
		t.Errorf("a second cycle sent %d mails", n-4)
	}

	// On 13 October HMS-kort has expired: expired mailed once more each.
	setClock(t, h, osloAt("2026-10-13", 7))
	runCompetence(t, h)
	if e, x := stamps(t, h, hms); !e || !x {
		t.Errorf("HMS-kort stamps after its end = expiring %v, expired %v; want both", e, x)
	}
	if n := len(mailsTo(h, kariAddress)); n != 4 {
		t.Errorf("%d mails to Kari after the truck certificate and HMS-kort expired, want 4", n)
	}
	if m := lastMail(t, h, bossAddress); !strings.Contains(m, "“HMS card” of Kari Nordmann has expired") {
		t.Errorf("Bjørn's last mail:\n%s", m)
	}

	// Nothing logged names an address; the cycle logs its counts.
	logs := scrubRandom(h.Logs())
	leaksIn(t, "the logs", logs, kariAddress, bossAddress)
	if !strings.Contains(logs, "the competence cycle ran") || !strings.Contains(logs, `"stamped"`) {
		t.Errorf("the cycle's counts are not logged:\n%s", logs)
	}
}

// A renewal supersedes the stamped row: the old row is never mailed again,
// the new one starts unstamped and is mailed once on its own lead.
func TestCompetenceWorker_RenewalAndSupersededRows(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorld(t)
	h := w.h
	old := rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: catalogueID(t, h, "hms-kort"), from: "2024-10-01", to: strPtr("2026-09-30"), verified: true})
	runCompetence(t, h)
	if n := len(h.mail.messages()); n != 2 {
		t.Fatalf("%d mails about the old row, want two", n)
	}
	renewed := renewRow(t, h, old, "2026-11-01")
	setClock(t, h, osloAt("2026-10-01", 7))
	runCompetence(t, h)
	if e, x := stamps(t, h, old); !e || x {
		t.Errorf("the superseded row's stamps = expiring %v, expired %v; want its expiring stamp alone", e, x)
	}
	if e, x := stamps(t, h, renewed); !e || x {
		t.Errorf("the renewed row's stamps = expiring %v, expired %v; want expiring", e, x)
	}
	if n := len(h.mail.messages()); n != 4 {
		t.Fatalf("%d mails in all, want the old row's two and the new row's two", n)
	}
	if m := lastMail(t, h, kariAddress); !strings.Contains(m, "2026-11-01") {
		t.Errorf("the renewed row's mail:\n%s", m)
	}
}

// A new end clears the stamps: a row mailed as expiring whose end HR moves a
// year ahead is not mailed again now, and is mailed again once the new end
// comes within its lead.
func TestCompetenceWorker_ANewEndClearsTheStamps(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorld(t)
	h := w.h
	hr, _ := signIn(t, h, "staff:manage")
	// HMS-kort's lead is 60 days: 12 October is within it.
	row := rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: catalogueID(t, h, "hms-kort"), from: "2024-10-13", to: strPtr("2026-10-12"), verified: true})
	runCompetence(t, h)
	if e, _ := stamps(t, h, row); !e || len(h.mail.messages()) != 2 {
		t.Fatalf("expiring stamped %v, %d mails; want stamped and two mails", e, len(h.mail.messages()))
	}

	revision := modtest.One[int32](t, h.Harness, `SELECT revision FROM staff.person_competences WHERE id = $1`, row)
	do(t, hr, http.MethodPut, personCompetencePath(row), map[string]any{"validTo": "2027-10-12"}, http.StatusOK, nil, ifMatch(revision))
	if e, x := stamps(t, h, row); e || x {
		t.Errorf("stamps after the new end = expiring %v, expired %v; want neither", e, x)
	}
	runCompetence(t, h)
	if n := len(h.mail.messages()); n != 2 {
		t.Errorf("%d mails after the new end, want no new one a year ahead", n)
	}

	setClock(t, h, osloAt("2027-08-20", 7))
	runCompetence(t, h)
	if e, _ := stamps(t, h, row); !e || len(h.mail.messages()) != 4 {
		t.Fatalf("on 20 August 2027: expiring stamped %v, %d mails; want stamped and two more", e, len(h.mail.messages()))
	}
	if m := lastMail(t, h, kariAddress); !strings.Contains(m, "2027-10-12") {
		t.Errorf("Kari's mail about the new end:\n%s", m)
	}
	// A change that keeps the end keeps the stamps. (A year on, HR signs in
	// again.)
	hr, _ = signIn(t, h, "staff:manage")
	revision = modtest.One[int32](t, h.Harness, `SELECT revision FROM staff.person_competences WHERE id = $1`, row)
	do(t, hr, http.MethodPut, personCompetencePath(row), map[string]any{"validTo": "2027-10-12", "reference": "HMS-1"}, http.StatusOK, nil, ifMatch(revision))
	if e, _ := stamps(t, h, row); !e {
		t.Error("a change that kept the end cleared the expiring stamp")
	}
}

// An erased person and a leaver are neither stamped nor mailed, nor is a
// person with no manager mailed through anyone else; a person without an
// address is stamped and logged by id.
func TestCompetenceWorker_SkipsErasedAndLeavers(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorld(t)
	h := w.h
	fse := catalogueID(t, h, "fse")
	erased := seedPerson(t, h, "Ola", "Glemt")
	seedEmployment(t, h, erased, w.boss)
	h.Exec(t, `UPDATE staff.people SET erased_at = $2, private_email = 'ola@example.test' WHERE id = $1`, erased, h.Now())
	erasedRow := rawPersonCompetence(t, h, competenceRow{person: erased, competence: fse, from: "2025-09-02", to: strPtr("2026-09-01"), verified: true})
	leaver := seedPerson(t, h, "Per", "Sluttet")
	seedEmploymentDated(t, h, leaver, w.boss, "2024-01-01", dayPtr("2026-08-31"))
	h.Exec(t, `UPDATE staff.people SET private_email = 'per@example.test' WHERE id = $1`, leaver)
	leaverRow := rawPersonCompetence(t, h, competenceRow{person: leaver, competence: fse, from: "2025-09-02", to: strPtr("2026-09-01"), verified: true})
	silent := seedPerson(t, h, "Siri", "Stille")
	seedEmployment(t, h, silent, 0)
	silentRow := rawPersonCompetence(t, h, competenceRow{person: silent, competence: fse, from: "2025-09-02", to: strPtr("2026-09-01"), verified: true})

	runCompetence(t, h)
	for _, row := range []int64{erasedRow, leaverRow} {
		if e, x := stamps(t, h, row); e || x {
			t.Errorf("row %d of an erased person or a leaver stamped", row)
		}
	}
	if _, x := stamps(t, h, silentRow); !x {
		t.Error("the row of a person without an address was not stamped")
	}
	if n := len(h.mail.messages()); n != 0 {
		t.Errorf("%d mails, want none", n)
	}
	if !strings.Contains(h.Logs(), "the recipient has no email") {
		t.Error("the skipped mail is not logged")
	}
}

// A mail server refusing every mail fails no cycle: the rows are stamped, the
// failures counted and logged with the person's id and the recipient
// redacted, and nothing is retried.
func TestCompetenceWorker_AFailingSendFailsNothing(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorld(t)
	h := w.h
	row := rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: catalogueID(t, h, "fse"), from: "2025-09-02", to: strPtr("2026-09-01"), verified: true})
	h.mail.failWith(func(out mail.Outbound) error {
		return errors.New("550 mailbox " + strings.ToUpper(out.To[0]) + " unavailable")
	})
	runCompetence(t, h)
	if _, x := stamps(t, h, row); !x {
		t.Error("the row was not stamped")
	}
	logs := scrubRandom(h.Logs())
	leaksIn(t, "the logs", logs, kariAddress, bossAddress)
	if !strings.Contains(logs, "a competence mail could not be sent") || !strings.Contains(logs, "<recipient>") ||
		!strings.Contains(logs, `"failed":2`) {
		t.Errorf("the failed sends are not logged as expected:\n%s", logs)
	}
	h.mail.failWith(nil)
	runCompetence(t, h)
	if n := len(h.mail.messages()); n != 0 {
		t.Errorf("a failed send was retried: %d mails", n)
	}
}

// With the log mail driver nothing is sent, and the rows are stamped all the
// same: the dashboard's item is the durable signal.
func TestCompetenceWorker_NoMailWithoutSMTP(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorld(t, modtest.WithEnv("MAIL_DRIVER", "log"))
	h := w.h
	row := rawPersonCompetence(t, h, competenceRow{person: w.kari, competence: catalogueID(t, h, "fse"), from: "2025-09-02", to: strPtr("2026-09-01"), verified: true})
	runCompetence(t, h)
	if _, x := stamps(t, h, row); !x {
		t.Error("the row was not stamped")
	}
	if n := len(h.mail.messages()); n != 0 {
		t.Errorf("%d mails with the log driver", n)
	}
}
