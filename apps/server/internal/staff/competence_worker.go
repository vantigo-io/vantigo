package staff

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the staff-competence worker (staff competence design S7, §4
// "The worker", §9 "The worker and a mail server down"; plan Task 3b,
// reading 4). Every poll it reads every current row with an end, of a person
// not erased and in employment today in the business zone, and judges it:
//
//   - valid_to before today and expired_notified_at unset → expired;
//   - else, a lead set on the competence, valid_to no more than the lead's
//     days after today and expiring_notified_at unset → expiring (a lead of
//     null never fires).
//
// Each row so judged is stamped in a short transaction of its own, under the
// row's lock and judged again there, so two replicas stamp it once — and
// stamped BEFORE any mail: the stamp is the run's, not the send's (reading
// 4). Once every transaction is closed the mails go, one after the other
// under one budget of competenceMailTimeout for the cycle, through smtpSend:
// to the person and to each of their manager-deciders (deciders.go), at the
// work email Staff holds, else the private one, in each one's language. A
// send that fails is logged — the person's id, the recipient redacted — and
// never retried, nor does it fail the cycle: the dashboard's item, computed
// on read (competence_attention.go), is the durable signal (reading 5). A
// cycle a second time finds the stamps and sends nothing; a renewal
// supersedes the row, and the new row starts unstamped.

const (
	// competenceWorkerName is what the runner logs this worker as.
	competenceWorkerName = "staff-competence"

	// competenceMailTimeout bounds one cycle's sends, all of them together.
	competenceMailTimeout = 30 * time.Second
)

// CompetenceWorker mails the people whose certificates expire, and their
// managers. It implements worker.Worker.
type CompetenceWorker struct {
	deps module.Deps
	srv  *server
}

var _ worker.Worker = (*CompetenceWorker)(nil)

// NewCompetenceWorker builds the worker over d.
func NewCompetenceWorker(d module.Deps) *CompetenceWorker {
	return &CompetenceWorker{deps: d, srv: newServer(d)}
}

// Name identifies this worker in the runner's logs.
func (w *CompetenceWorker) Name() string { return competenceWorkerName }

// Interval is the poll (STAFF_WORKERS_POLL, default an hour).
func (w *CompetenceWorker) Interval() time.Duration { return w.srv.workersPoll() }

// Run is the worker loop: a cycle, then the poll. A failure is logged and
// the loop goes on.
func (w *CompetenceWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.srv.logger().Error("staff: the competence cycle failed", "worker", competenceWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one cycle.
func (w *CompetenceWorker) RunCycle(ctx context.Context) error {
	return w.srv.competenceCycle(ctx)
}

// competenceNotice is one row the cycle stamped, and so mails about.
type competenceNotice struct {
	kind           string
	rowID          int64
	personID       int64
	nameEn, nameNb string
	validTo        time.Time
}

// competenceMailTo is one mail of a cycle: the address, the words, and the
// person the log line names when it cannot be sent.
type competenceMailTo struct {
	to, subject, body string
	personID          int64
}

// competenceCycle is one cycle: judge, stamp, then mail.
func (s *server) competenceCycle(ctx context.Context) error {
	zone, err := s.settingsZone(ctx)
	if err != nil {
		return err
	}
	today := civilToday(s.deps.Clock(), zone)
	q := store.New(s.deps.Pool)
	rows, err := q.ListExpiringCompetences(ctx)
	if err != nil {
		return fmt.Errorf("staff: read the competences that expire: %w", err)
	}
	activeIDs, err := q.PeopleActiveOn(ctx, dayParam(today))
	if err != nil {
		return fmt.Errorf("staff: read the people active today: %w", err)
	}
	active := make(map[int64]bool, len(activeIDs))
	for _, id := range activeIDs {
		active[id] = true
	}
	var notices []competenceNotice
	for _, r := range rows {
		pc := r.StaffPersonCompetence
		if r.PersonErasedAt != nil || !active[pc.PersonID] || competenceNoticeDue(pc, r.CompetenceReminderLeadDays, today) == "" {
			continue
		}
		kind, err := s.stampCompetence(ctx, pc.ID, r.CompetenceReminderLeadDays, today)
		if err != nil {
			return err
		}
		if kind == "" {
			continue
		}
		notices = append(notices, competenceNotice{
			kind: kind, rowID: pc.ID, personID: pc.PersonID,
			nameEn: r.CompetenceNameEn, nameNb: r.CompetenceNameNb, validTo: civil(pc.ValidTo),
		})
	}
	mailed, failed := 0, 0
	switch {
	case len(notices) == 0:
	case !s.mailAvailable():
		s.logger().InfoContext(ctx, "staff: competence mails skipped: the mail driver is not smtp",
			"worker", competenceWorkerName, "notices", len(notices))
	default:
		mailed, failed = s.sendCompetenceMails(ctx, s.competenceMails(ctx, notices, today))
	}
	s.logger().InfoContext(ctx, "staff: the competence cycle ran", "worker", competenceWorkerName,
		"stamped", len(notices), "mailed", mailed, "failed", failed)
	return nil
}

// competenceNoticeDue is the notice row is due at today, "" for none, with
// lead its competence's reminder_lead_days.
func competenceNoticeDue(row store.StaffPersonCompetence, lead *int16, today time.Time) string {
	if row.SupersededByID != nil || !row.ValidTo.Valid {
		return ""
	}
	validTo := civil(row.ValidTo)
	if validTo.Before(today) {
		if row.ExpiredNotifiedAt == nil {
			return competenceExpiredNotice
		}
		return ""
	}
	if lead != nil && row.ExpiringNotifiedAt == nil && int(validTo.Sub(today).Hours()/24) <= int(*lead) {
		return competenceExpiringNotice
	}
	return ""
}

// stampCompetence takes row id's lock, judges it again on what it holds —
// another replica may have stamped it, a renewal superseded it or a change
// moved its end since the list was read — and stamps the notice due, which
// it answers ("" for none, and for a row gone).
func (s *server) stampCompetence(ctx context.Context, id int64, lead *int16, today time.Time) (string, error) {
	kind := ""
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, q *store.Queries) error {
		row, err := q.LockPersonCompetence(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("staff: lock person competence %d: %w", id, err)
		}
		now := s.deps.Clock()
		switch kind = competenceNoticeDue(row, lead, today); kind {
		case competenceExpiredNotice:
			err = q.MarkCompetenceExpiredNotified(ctx, store.MarkCompetenceExpiredNotifiedParams{Now: now, ID: id})
		case competenceExpiringNotice:
			err = q.MarkCompetenceExpiringNotified(ctx, store.MarkCompetenceExpiringNotifiedParams{Now: now, ID: id})
		}
		if err != nil {
			return fmt.Errorf("staff: stamp person competence %d: %w", id, err)
		}
		return nil
	})
	return kind, err
}

// competenceMails is every notice's mails: the person's, and each of their
// manager-deciders' on today, at the address Staff holds — the work email,
// else the private one — in the recipient's language. Each person's
// managers are read once; a read that fails skips that person's mails with
// a log line.
func (s *server) competenceMails(ctx context.Context, notices []competenceNotice, today time.Time) []competenceMailTo {
	logger := s.logger()
	managers := map[int64][]int64{}
	ids := []int64{}
	for _, n := range notices {
		if _, ok := managers[n.personID]; ok {
			continue
		}
		got, err := s.managerDeciders(ctx, n.personID, today)
		if err != nil {
			logger.WarnContext(ctx, "staff: competence mail to the managers skipped: the managers could not be read",
				"worker", competenceWorkerName, "person_id", n.personID, "error", err.Error())
		}
		managers[n.personID] = got
		ids = append(ids, n.personID)
		ids = append(ids, got...)
	}
	rows, err := store.New(s.deps.Pool).MailRecipients(ctx, ids)
	if err != nil {
		logger.WarnContext(ctx, "staff: competence mails skipped: the recipients could not be read",
			"worker", competenceWorkerName, "error", err.Error())
		return nil
	}
	byID := make(map[int64]store.MailRecipientsRow, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	var out []competenceMailTo
	for _, n := range notices {
		person := byID[n.personID]
		recipients := []int64{n.personID}
		for _, m := range managers[n.personID] {
			if m != n.personID {
				recipients = append(recipients, m)
			}
		}
		for _, id := range recipients {
			r, ok := byID[id]
			to := r.WorkEmail
			if to == nil {
				to = r.PrivateEmail
			}
			if !ok || to == nil {
				logger.InfoContext(ctx, "staff: competence mail skipped: the recipient has no email",
					"worker", competenceWorkerName, "person_id", id, "notice", n.kind)
				continue
			}
			subjectName, link := "", s.appLink(mePage)
			if id != n.personID {
				subjectName = person.DisplayName
				link = s.appLink("/staff/people/" + strconv.FormatInt(n.personID, 10) + "?tab=competence")
			}
			subject, body := competenceMail(n.kind, r.PreferredLanguage, r.DisplayName, subjectName, n.nameEn, n.nameNb, n.validTo, link)
			out = append(out, competenceMailTo{to: *to, subject: subject, body: body, personID: id})
		}
	}
	return out
}

// sendCompetenceMails sends mails one after the other under one budget of
// competenceMailTimeout, and answers how many were sent and how many were
// not — failed, or left unsent when the budget ran out, which is logged by
// count.
func (s *server) sendCompetenceMails(ctx context.Context, mails []competenceMailTo) (sent, failed int) {
	if len(mails) == 0 {
		return 0, 0
	}
	logger := s.logger()
	budget, cancel := context.WithTimeout(ctx, competenceMailTimeout)
	defer cancel()
	for i, m := range mails {
		if budget.Err() != nil {
			logger.WarnContext(ctx, "staff: competence mails not sent: the time for the cycle ran out",
				"worker", competenceWorkerName, "unsent", len(mails)-i)
			return sent, failed + len(mails) - i
		}
		err := s.smtpSend(budget, s.deps.Config.Mail, mail.Outbound{To: []string{m.to}, Subject: m.subject, TextBody: m.body})
		if err != nil {
			failed++
			redact := regexp.MustCompile("(?i)" + regexp.QuoteMeta(m.to))
			logger.WarnContext(ctx, "staff: a competence mail could not be sent", "worker", competenceWorkerName,
				"person_id", m.personID, "error", redact.ReplaceAllLiteralString(err.Error(), "<recipient>"))
			continue
		}
		sent++
	}
	return sent, failed
}
