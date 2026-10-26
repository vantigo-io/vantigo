package planning

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// The attention items (design S22), computed on read, each to its audience:
// unbooked, overbooked and certificatesExpiring to planning:view,
// endingWithoutFollowOn, requestsOpen and wishesUnseen to planning:manage. A caller with
// neither role gets an empty list, never a 403: the host asks every module it
// shows.

// followOnDays is how soon after a confirmed row's end the person's next
// confirmed row must start for the first to have a follow-on (S22).
const followOnDays = 7

// GetPlanningStatsAttention Planning attention items
// (GET /api/v1/planning/stats/attention)
//
// planning:access, shaped per caller; oldest first, then by type and id. A
// bare array, as every module's attention is (amendment 40).
func (s *server) GetPlanningStatsAttention(ctx context.Context, _ gen.GetPlanningStatsAttentionRequestObject) (gen.GetPlanningStatsAttentionResponseObject, error) {
	items, err := s.attention(ctx)
	if status, problem := readProblem(err); status == http.StatusServiceUnavailable {
		return gen.GetPlanningStatsAttention503ApplicationProblemPlusJSONResponse(problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetPlanningStatsAttention200JSONResponse(items), nil
}

// attention is the caller's items over one window, read once: the Monday of
// this week — the overbooked weeks are whole weeks — to the later of the
// Sunday ending the unbooked horizon and the ending lead's end plus the
// follow-on days.
func (s *server) attention(ctx context.Context) ([]gen.PlanningStatsAttentionItem, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return nil, err
	}
	items := []gen.PlanningStatsAttentionItem{}
	if !c.View && !c.Manage {
		return items, nil
	}
	row, err := settings(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	horizonFrom, horizonTo := horizonOf(c.Today, row)
	endingTo := addDays(c.Today, int(row.EndingLeadWeeks)*7)
	end := sundayOf(horizonTo)
	if followOn := addDays(endingTo, followOnDays); followOn.After(end) {
		end = followOn
	}
	w, err := s.loadWindow(ctx, mondayOf(c.Today), end, nil)
	if err != nil {
		return nil, err
	}

	if c.View {
		held, err := s.workforceHeld(ctx, c.Today, personIDsOf(w.People))
		if err != nil {
			return nil, err
		}
		for _, u := range unbookedIn(w, horizonFrom, horizonTo, row.UnbookedThresholdPercent) {
			occurred := c.Today
			if u.RollOff != nil {
				occurred = *u.RollOff
			}
			items = append(items, attentionItem(gen.PlanningStatsAttentionItemTypeUnbooked, strconv.FormatInt(u.Person.ID, 10),
				u.Person.ID, u.Person.DisplayName, occurred))
		}
		items = append(items, overbookedItems(w, horizonTo)...)
		items = append(items, certificateItems(w, held, horizonFrom, horizonTo)...)
	}
	if c.Manage {
		ending, err := s.endingItems(ctx, w, c.Today, endingTo)
		if err != nil {
			return nil, err
		}
		items = append(items, ending...)
		open, err := s.requestItems(ctx, c.Today, row.RequestLeadDays)
		if err != nil {
			return nil, err
		}
		items = append(items, open...)
		unseen, err := s.unseenWishesItem(ctx, w)
		if err != nil {
			return nil, err
		}
		items = append(items, unseen...)
	}
	slices.SortFunc(items, func(a, b gen.PlanningStatsAttentionItem) int {
		return cmp.Or(a.OccurredAt.Compare(b.OccurredAt), cmp.Compare(a.Type, b.Type), cmp.Compare(a.Id, b.Id))
	})
	return items, nil
}

// attentionItem is one item of kind, keyed by key, about entityID, named by
// title, dated on the civil day occurred.
func attentionItem(kind gen.PlanningStatsAttentionItemType, key string, entityID int64, title string, occurred time.Time) gen.PlanningStatsAttentionItem {
	return gen.PlanningStatsAttentionItem{
		Id: string(kind) + "/" + key, Type: kind, Title: title,
		OccurredAt: occurred, EntityId: strconv.FormatInt(entityID, 10),
	}
}

// overbookedItems is one item per person with a whole week, from this
// week's to the one the horizon ends in, whose confirmed booked exceeds its
// capacity (S7's rule, S22): dated on the first such week's Monday and
// titled with its ISO week.
func overbookedItems(w *window, horizonTo time.Time) []gen.PlanningStatsAttentionItem {
	var out []gen.PlanningStatsAttentionItem
	for _, e := range w.People {
		in := w.inputOf(e.ID, false, nil)
		in.To = sundayOf(horizonTo)
		for _, week := range capacityWeeks(in) {
			if hundredths(week.Booked) <= hundredths(week.Capacity) {
				continue
			}
			_, number := week.Monday.ISOWeek()
			out = append(out, attentionItem(gen.PlanningStatsAttentionItemTypeOverbooked, strconv.FormatInt(e.ID, 10),
				e.ID, fmt.Sprintf("%s, week %d", e.DisplayName, number), week.Monday))
			break
		}
	}
	return out
}

// certificateItems is one item per confirmed row and held certificate
// expiring inside it within the horizon (S12's flag, S22), dated on the
// certificate's validTo and titled with the person and the certificate.
func certificateItems(w *window, held map[int64][]contracts.HeldCompetence, from, to time.Time) []gen.PlanningStatsAttentionItem {
	var out []gen.PlanningStatsAttentionItem
	for _, e := range w.People {
		for _, a := range w.rowsOf(e.ID) {
			if a.Status != statusConfirmed {
				continue
			}
			for _, h := range expiringCertificates(a, held[e.ID]) {
				if h.ValidTo.Before(from) || h.ValidTo.After(to) {
					continue
				}
				key := fmt.Sprintf("%d/%d", a.ID, h.CompetenceID)
				out = append(out, attentionItem(gen.PlanningStatsAttentionItemTypeCertificatesExpiring, key,
					e.ID, fmt.Sprintf("%s, %s", e.DisplayName, h.Key), *h.ValidTo))
			}
		}
	}
	return out
}

// endingItems is one item per confirmed row ending from today to endingTo
// whose person has no other confirmed row running past its end and starting
// on or before that end plus the follow-on days (S22): about the allocation,
// dated on its last day, titled with the person and the project's code.
func (s *server) endingItems(ctx context.Context, w *window, today, endingTo time.Time) ([]gen.PlanningStatsAttentionItem, error) {
	var ending []store.PlanningAllocation
	for _, e := range w.People {
		rows := w.rowsOf(e.ID)
		for _, a := range rows {
			last := civil(a.ToDate)
			if a.Status != statusConfirmed || last.Before(today) || last.After(endingTo) {
				continue
			}
			followed := slices.ContainsFunc(rows, func(o store.PlanningAllocation) bool {
				return o.ID != a.ID && o.Status == statusConfirmed &&
					civil(o.ToDate).After(last) && !civil(o.FromDate).After(addDays(last, followOnDays))
			})
			if !followed {
				ending = append(ending, a)
			}
		}
	}
	projects, err := s.projectsOfRows(ctx, ending)
	if err != nil {
		return nil, err
	}
	names := peopleByID(w.People)
	out := make([]gen.PlanningStatsAttentionItem, 0, len(ending))
	for _, a := range ending {
		title := names[a.PersonID].DisplayName
		if p, ok := projects[a.ProjectID]; ok {
			title = fmt.Sprintf("%s, %s", title, p.Code)
		}
		out = append(out, attentionItem(gen.PlanningStatsAttentionItemTypeEndingWithoutFollowOn, strconv.FormatInt(a.ID, 10),
			a.ID, title, civil(a.ToDate)))
	}
	return out, nil
}

// requestItems is one requestsOpen item per open request starting within
// leadDays of today, or already started (S22): about the request, dated on
// its first day, titled "CODE — title" with the project's code when the
// directory resolves it, else its id.
func (s *server) requestItems(ctx context.Context, today time.Time, leadDays int16) ([]gen.PlanningStatsAttentionItem, error) {
	rows, err := store.New(s.deps.Pool).OpenRequestsStartingBy(ctx, dayParam(addDays(today, int(leadDays))))
	if err != nil {
		return nil, fmt.Errorf("planning: read the open requests within the lead: %w", err)
	}
	ids := make([]int32, len(rows))
	for i, r := range rows {
		ids[i] = r.ProjectID
	}
	projects, err := s.projectEntries(ctx, slices.Compact(slices.Sorted(slices.Values(ids))))
	if err != nil {
		return nil, err
	}
	out := make([]gen.PlanningStatsAttentionItem, 0, len(rows))
	for _, r := range rows {
		code := strconv.FormatInt(int64(r.ProjectID), 10)
		if p, ok := projects[r.ProjectID]; ok {
			code = p.Code
		}
		out = append(out, attentionItem(gen.PlanningStatsAttentionItemTypeRequestsOpen, strconv.FormatInt(r.ID, 10),
			r.ID, fmt.Sprintf("%s — %s", code, r.Title), civil(r.FromDate)))
	}
	return out, nil
}

// unseenWishesItem is S22's wishesUnseen: one item when any open wish has no
// seen stamp, counting them (plan reading 9) — about the oldest, dated on
// its creation, titled with its person's name as the window's people give
// it, or their id when Staff does not answer them. The Wishes page is the
// answer, not a list of names on the dashboard.
func (s *server) unseenWishesItem(ctx context.Context, w *window) ([]gen.PlanningStatsAttentionItem, error) {
	rows, err := store.New(s.deps.Pool).UnseenWishes(ctx)
	if err != nil {
		return nil, fmt.Errorf("planning: read the unseen wishes: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	oldest := rows[0]
	title := strconv.FormatInt(oldest.PersonID, 10)
	if p := peopleByID(w.People)[oldest.PersonID]; p != nil {
		title = p.DisplayName
	}
	count := int32(len(rows))
	item := attentionItem(gen.PlanningStatsAttentionItemTypeWishesUnseen, "all", oldest.ID, title, oldest.CreatedAt)
	item.Count = &count
	return []gen.PlanningStatsAttentionItem{item}, nil
}
