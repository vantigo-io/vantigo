package staff

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the competence review (staff competence design S11; the
// one-to-one plan, Task 3b): a stamp that the person and whoever may verify
// their competence went through the Competence tab together — a date and the
// reviewer, no notes. Whoever may verify records one (verifierRefusal), never
// the person on their own (own_record); the Reviews group's audiences read
// them (Self, Manager, Approver, HR, personal-data), the newest first, and
// the person's response carries the latest as lastCompetenceReview. The
// overdue item it clears is competence_attention.go's.

// invalidCompetenceReviewTitle is the 400's title for a review refused on
// its fields.
const invalidCompetenceReviewTitle = "Invalid competence review"

// GetStaffPeopleByIdCompetenceReviews List a person's competence reviews
// (GET /api/v1/staff/people/{id}/competence-reviews)
//
// The Reviews group's audiences; anyone else — a colleague, a caller who
// does not see the person — the 404. The newest first, each reviewer named
// as the user directory names them, read once per user after the
// transaction.
func (s *server) GetStaffPeopleByIdCompetenceReviews(ctx context.Context, req gen.GetStaffPeopleByIdCompetenceReviewsRequestObject) (gen.GetStaffPeopleByIdCompetenceReviewsResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var rows []store.StaffCompetenceReview
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := readPerson(ctx, q, req.Id); err != nil {
			return err
		}
		a, err := personAudience(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		if !a.reads(groupReviews) {
			return notFound()
		}
		if rows, err = q.ListCompetenceReviews(ctx, req.Id); err != nil {
			return fmt.Errorf("staff: list person %d's competence reviews: %w", req.Id, err)
		}
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdCompetenceReviews404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ReviewerUserID)
	}
	names, err := s.reviewerNames(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]gen.StaffCompetenceReview, 0, len(rows))
	for _, r := range rows {
		out = append(out, competenceReviewResponse(r, names))
	}
	return gen.GetStaffPeopleByIdCompetenceReviews200JSONResponse(out), nil
}

// PostStaffPeopleByIdCompetenceReviews Record a competence review
// (POST /api/v1/staff/people/{id}/competence-reviews)
//
// Whoever may verify the person's competence (§4): a manager of theirs
// today, staff:approve or staff:manage, never the person (own_record). The
// refusals come in the order that tells a caller least, as a competence
// row's add does: the person outside the Reviews group (a colleague, a
// caller who does not see them) 404, the verifier's rule 403, a property the
// request does not take 400, the date 400, an erased person 409. reviewedOn left out is today in the business time
// zone; a later day is refused. The reviewer is the caller, and their linked
// person when they have one.
func (s *server) PostStaffPeopleByIdCompetenceReviews(ctx context.Context, req gen.PostStaffPeopleByIdCompetenceReviewsRequestObject) (gen.PostStaffPeopleByIdCompetenceReviewsResponseObject, error) {
	var body gen.StaffCompetenceReviewRequest
	if req.Body != nil {
		body = *req.Body
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := readPerson(ctx, q, req.Id); err != nil {
			return err
		}
		return judgeReviewer(ctx, q, caller, req.Id)
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusForbidden {
			return reviewRefused(err), nil
		}
		return gen.PostStaffPeopleByIdCompetenceReviews404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	if f := s.unknownProperties(ctx, competenceReviewRequestSchema); f.failed() {
		return gen.PostStaffPeopleByIdCompetenceReviews400ApplicationProblemPlusJSONResponse(invalid(invalidCompetenceReviewTitle, f)), nil
	}
	reviewedOn := caller.Today
	if d := civilDate(body.ReviewedOn); d != nil {
		reviewedOn = d.Time
	}
	if reviewedOn.After(caller.Today) {
		var f fieldErrors
		f.add("reviewedOn", "A review is dated today or earlier")
		return gen.PostStaffPeopleByIdCompetenceReviews400ApplicationProblemPlusJSONResponse(invalid(invalidCompetenceReviewTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var saved store.StaffCompetenceReview
	err = s.withPersonLock(ctx, req.Id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := judgeReviewer(ctx, txq, caller, req.Id); err != nil {
			return err
		}
		var err error
		saved, err = txq.InsertCompetenceReview(ctx, store.InsertCompetenceReviewParams{
			PersonID: req.Id, ReviewedOn: dayParam(reviewedOn), ReviewerUserID: caller.UserID,
			ReviewerPersonID: caller.Person, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("staff: record a competence review of person %d: %w", req.Id, err)
		}
		return s.recordChanges(ctx, txq, req.Id, eventCompetenceReviewed, who,
			changes{"reviewedOn": fieldChange{To: openapi_types.Date{Time: reviewedOn}}})
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPeopleByIdCompetenceReviews404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PostStaffPeopleByIdCompetenceReviews404Response{}, nil
		case http.StatusForbidden:
			return reviewRefused(err), nil
		default:
			return gen.PostStaffPeopleByIdCompetenceReviews409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, err
	}
	names, err := s.reviewerNames(ctx, []uuid.UUID{saved.ReviewerUserID})
	if err != nil {
		return nil, err
	}
	return gen.PostStaffPeopleByIdCompetenceReviews201JSONResponse(competenceReviewResponse(saved, names)), nil
}

// judgeReviewer is the review's rule for caller on personID, on q: the 404
// outside the Reviews group, then the verifier's refusals.
func judgeReviewer(ctx context.Context, q *store.Queries, caller *callerContext, personID int64) error {
	a, err := personAudience(ctx, q, caller, personID)
	if err != nil {
		return err
	}
	if !a.reads(groupReviews) {
		return notFound()
	}
	if a.Self {
		return refuseForbidden(codeOwnRecord, "Your own review",
			"You cannot record a review of your own competence. Your manager, staff:approve or HR records it.")
	}
	return verifierRefusal(a)
}

// reviewRefused is a 403 refusal as the review's POST answers it: with its
// problem for own_record, the access layer's body otherwise.
func reviewRefused(err error) gen.PostStaffPeopleByIdCompetenceReviewsResponseObject {
	if r, ok := asRefusal(err); ok && r.problem.Code != nil {
		return gen.PostStaffPeopleByIdCompetenceReviews403ApplicationProblemPlusJSONResponse(r.problem)
	}
	return gen.PostStaffPeopleByIdCompetenceReviews403JSONResponse(apicommon.ForbiddenBody())
}

// reviewerNames is the reviewers' names as the user directory names them,
// each user asked once — read outside any transaction. A user the directory
// no longer knows is left out, and named "Unknown user" by reviewerName.
func (s *server) reviewerNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	var distinct []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			distinct = append(distinct, id)
		}
	}
	users, err := s.usersUsers(ctx, distinct)
	if err != nil {
		return nil, fmt.Errorf("staff: read the reviewers' names: %w", err)
	}
	out := make(map[uuid.UUID]string, len(users))
	for _, u := range users {
		out[u.ID] = u.DisplayName
	}
	return out, nil
}

// reviewerName is id's name in names, "Unknown user" when the directory no
// longer knows them.
func reviewerName(names map[uuid.UUID]string, id uuid.UUID) string {
	if name, ok := names[id]; ok {
		return name
	}
	return unknownUserDisplay
}

// competenceReviewResponse is a review as the contract answers it.
func competenceReviewResponse(r store.StaffCompetenceReview, names map[uuid.UUID]string) gen.StaffCompetenceReview {
	return gen.StaffCompetenceReview{
		Id: r.ID, ReviewedOn: openapi_types.Date{Time: r.ReviewedOn.Time}, Reviewer: reviewerName(names, r.ReviewerUserID),
		RecordedAt: r.CreatedAt,
	}
}

// lastCompetenceReview is the person's latest review for an audience that
// reads the Reviews group, nil for anyone else or when there is none — read
// on the pool and the reviewer named after it, so never inside a
// transaction.
func (s *server) lastCompetenceReview(ctx context.Context, a audience, personID int64) (*gen.StaffLastCompetenceReview, error) {
	if !a.reads(groupReviews) {
		return nil, nil
	}
	row, err := store.New(s.deps.Pool).LatestCompetenceReview(ctx, personID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: read person %d's latest competence review: %w", personID, err)
	}
	names, err := s.reviewerNames(ctx, []uuid.UUID{row.ReviewerUserID})
	if err != nil {
		return nil, err
	}
	return &gen.StaffLastCompetenceReview{
		ReviewedOn: openapi_types.Date{Time: row.ReviewedOn.Time}, Reviewer: reviewerName(names, row.ReviewerUserID),
	}, nil
}
