package orders

// A subscription's schedule (orders design O43, O44): its periods judged
// against its dated lines, its live allocations and its skipped periods —
// which period is due, which is billed, and how far it is billed through. It
// reads nothing: the rows come in, the judgement goes out. The provider
// (billable_orders.go), the holder (invoiced_work.go) and the operations
// (subscriptions.go) all judge by it.

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// The subscription states (O42).
const (
	subscriptionDraft     = "draft"
	subscriptionActive    = "active"
	subscriptionPaused    = "paused"
	subscriptionEnded     = "ended"
	subscriptionCancelled = "cancelled"
)

// ownerSubscription is a subscription as the owner of lines, of a change log
// entry and as a source's kind.
const ownerSubscription = "subscription"

// The states of a period as the subscription page shows it (O43).
const (
	periodInvoiced = "invoiced"
	periodDraft    = "draft"
	periodSkipped  = "skipped"
	periodDue      = "due"
	periodPaused   = "paused"
	periodUpcoming = "upcoming"
)

// liveKey is a line's period: one live allocation each (the unique live
// period index).
type liveKey struct {
	lineID int64
	start  time.Time
}

// schedule is one subscription with everything its periods are judged by.
type schedule struct {
	sub      store.OrdersSubscription
	cal      calendar
	lines    []store.OrdersLine
	live     map[liveKey]store.OrdersInvoiceAllocation
	skips    map[time.Time]bool
	maxLive  time.Time
	hasLive  bool
	maxSkip  time.Time
	hasSkips bool
}

// newSchedule is sub's schedule over its lines (every dated version), its
// allocations (any state; only the draft and invoiced count) and its
// skipped period starts.
func newSchedule(sub store.OrdersSubscription, lines []store.OrdersLine, allocations []store.OrdersInvoiceAllocation, skips []pgtype.Date) schedule {
	cal, _ := newCalendar(sub.BillingInterval, sub.StartDate.Time, datePtr(sub.EndDate))
	cal.prorateEnd = sub.FinalPeriod == finalPeriodProrated
	s := schedule{sub: sub, cal: cal, lines: lines, live: map[liveKey]store.OrdersInvoiceAllocation{}, skips: map[time.Time]bool{}}
	for _, a := range allocations {
		if a.State != contracts.AllocationDraft && a.State != contracts.AllocationInvoiced || !a.PeriodStart.Valid {
			continue
		}
		start := civil(a.PeriodStart.Time)
		s.live[liveKey{a.LineID, start}] = a
		if !s.hasLive || start.After(s.maxLive) {
			s.maxLive, s.hasLive = start, true
		}
	}
	for _, d := range skips {
		if d.Valid {
			start := civil(d.Time)
			s.skips[start] = true
			if !s.hasSkips || start.After(s.maxSkip) {
				s.maxSkip, s.hasSkips = start, true
			}
		}
	}
	return s
}

// datePtr is a date column as a pointer, nil for NULL.
func datePtr(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	t := civil(d.Time)
	return &t
}

// inForce reports whether a dated line bills a period starting on day: it
// is valid on the period's first day (O44).
func inForce(l store.OrdersLine, day time.Time) bool {
	if l.ValidFrom.Valid && civil(l.ValidFrom.Time).After(day) {
		return false
	}
	if l.ValidTo.Valid && civil(l.ValidTo.Time).Before(day) {
		return false
	}
	return true
}

// billable reports whether a line bills at all: a priced product line with a
// VAT code.
func billable(l store.OrdersLine) bool {
	return l.Kind == kindProduct && l.Quantity.Valid && l.UnitPrice.Valid && l.VatCodeID != nil
}

// linesOn is the billable lines in force on a period starting on day, in
// position order.
func (s schedule) linesOn(day time.Time) []store.OrdersLine {
	var out []store.OrdersLine
	for _, l := range s.lines {
		if billable(l) && inForce(l, day) {
			out = append(out, l)
		}
	}
	return out
}

// billsFrom reports whether the subscription's status lets a period bill:
// active or ended — its periods up to the end — or paused, its periods
// before the pause.
func (s schedule) billsFrom(p period) bool {
	switch s.sub.Status {
	case subscriptionActive, subscriptionEnded:
		return true
	case subscriptionPaused:
		return s.sub.PausedFrom.Valid && p.start.Before(civil(s.sub.PausedFrom.Time))
	}
	return false
}

// duePeriod is one period with the lines it has left to bill.
type duePeriod struct {
	period period
	lines  []store.OrdersLine
}

// due is every period due through through and starting no later than
// twelve months after today (O43) — not skipped, and with a line in force on
// its first day that has no live allocation — with those lines, in order.
func (s schedule) due(through, today time.Time) []duePeriod {
	if s.cal.months == 0 {
		return nil
	}
	last := civil(through)
	if limit := latestBillableStart(today); limit.Before(last) {
		last = limit
	}
	var out []duePeriod
	for _, p := range s.cal.periodsThrough(last) {
		if !s.billsFrom(p) || s.skips[p.start] || !dueThrough(s.sub.Billing, p, through) {
			continue
		}
		var lines []store.OrdersLine
		for _, l := range s.linesOn(p.start) {
			if _, ok := s.live[liveKey{l.ID, p.start}]; !ok {
				lines = append(lines, l)
			}
		}
		if len(lines) > 0 {
			out = append(out, duePeriod{period: p, lines: lines})
		}
	}
	return out
}

// invoiced reports whether every line in force on a period has an invoiced
// allocation for it; a period with no line has nothing to bill and counts.
func (s schedule) invoiced(p period) bool {
	for _, l := range s.linesOn(p.start) {
		a, ok := s.live[liveKey{l.ID, p.start}]
		if !ok || a.State != contracts.AllocationInvoiced {
			return false
		}
	}
	return true
}

// billedThrough is the last day of the unbroken run, from the first period,
// of periods invoiced or skipped (O43); nil when the first is neither. It
// looks no further than the last period invoiced or skipped.
func (s schedule) billedThrough() *time.Time {
	if s.cal.months == 0 || (!s.hasLive && !s.hasSkips) {
		return nil
	}
	last := s.maxLive
	if s.hasSkips && s.maxSkip.After(last) {
		last = s.maxSkip
	}
	var through *time.Time
	for _, p := range s.cal.periodsThrough(last) {
		if !s.skips[p.start] && !s.invoiced(p) {
			break
		}
		end := p.end
		through = &end
	}
	return through
}

// lastLivePeriod is the last period with a live allocation, and whether
// there is one.
func (s schedule) lastLivePeriod() (period, bool) {
	if !s.hasLive {
		return period{}, false
	}
	return s.cal.periodOn(s.maxLive)
}

// livePeriod reports whether any line has a live allocation for the period
// starting on start.
func (s schedule) livePeriod(start time.Time) bool {
	for k := range s.live {
		if k.start.Equal(start) {
			return true
		}
	}
	return false
}

// firstOpenStart is the first period start a change may take effect from
// (O44): the start of the period after the last with a live allocation, or
// the first period's when none has one.
func (s schedule) firstOpenStart() time.Time {
	if p, ok := s.lastLivePeriod(); ok {
		return s.cal.period(p.index + 1).start
	}
	return s.cal.start
}
