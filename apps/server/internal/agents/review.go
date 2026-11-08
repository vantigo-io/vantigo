package agents

// The review queue (virtual employees design V20): what a run leaves for a
// person, listed, opened, approved, edited and sent, or discarded. Seeing an
// item needs what the agent read, not only agents:review: otherwise the queue
// would let a person read conversations and customers through the agent. Deciding
// needs the reviewer to satisfy the access rule of what the item does, by
// identity's UserSatisfies, so an approval never grants more than the reviewer
// could do.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/agents/gen"
	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// The states of a review item and its kinds (V20).
const (
	itemWaiting    = "waiting"
	itemDeciding   = "deciding" // claimed by a reviewer who is acting on it; shown as waiting
	filterDecided  = "decided"  // a filter, not a state: every state but waiting
	itemSent       = "sent"
	itemEditedSent = "edited_sent"
	itemDiscarded  = "discarded"
	itemWithdrawn  = "withdrawn"
	itemExpired    = "expired"

	kindReply  = "reply"
	kindChange = "change"
)

const (
	defaultPageSize = 50
	maxPageSize     = 200
	maxEditedText   = 20000
	maxDiscardText  = 2000
)

// itemRow is a review item with the names the screens show.
type itemRow = store.GetReviewItemRow

// itemView is a review item as the contract answers it.
func itemView(r itemRow) gen.ReviewItem {
	sources := []string{}
	_ = json.Unmarshal(r.Sources, &sources)
	if sources == nil {
		sources = []string{}
	}
	heldBy := r.HeldBy
	if heldBy == nil {
		heldBy = []string{}
	}
	state := r.State
	if state == itemDeciding {
		state = itemWaiting
	}
	v := gen.ReviewItem{
		Id: r.ID, RunId: r.RunID, AgentId: r.AgentID, AgentName: r.AgentName, RoleId: r.RoleID, RoleName: r.RoleName,
		ConversationId: r.ConversationID, Kind: gen.ReviewItemKind(r.Kind), State: gen.ReviewItemState(state),
		Score: int32(r.Score), ModelScore: int32(r.ModelScore), Sources: sources, HeldBy: heldBy,
		ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt, DecidedAt: r.DecidedAt, DecidedByUserId: r.DecidedByUserID,
		DiscardText: r.DiscardText, Outcome: r.Outcome, Version: r.Version,
	}
	if r.DiscardReason != nil {
		reason := gen.ReviewItemDiscardReason(*r.DiscardReason)
		v.DiscardReason = &reason
	}
	if r.EditRatio != nil {
		ratio := float64(*r.EditRatio)
		v.EditRatio = &ratio
	}
	switch r.Kind {
	case kindReply:
		var in storedReply
		_ = json.Unmarshal(r.Item, &in)
		reply := gen.ReviewReply{Text: in.Text, Disclosure: in.Disclosure}
		if in.InReplyTo != uuid.Nil {
			reply.InReplyToMessageId = &in.InReplyTo
		}
		if len(in.Components) > 0 && string(in.Components) != "null" {
			var c map[string]any
			if json.Unmarshal(in.Components, &c) == nil {
				reply.Components = &c
			}
		}
		v.Reply = &reply
		v.WindowClosesAt = in.WindowClosesAt
	case kindChange:
		var in storedChange
		_ = json.Unmarshal(r.Item, &in)
		change := gen.ReviewChange{OperationId: in.OperationID, Summary: in.Summary, Explain: in.Explain}
		change.PathParams = objectOf(in.PathParams)
		change.Query = objectOf(in.Query)
		change.Body = objectOf(in.Body)
		v.Change = &change
	}
	return v
}

// objectOf decodes a JSON object, nil for anything else.
func objectOf(raw json.RawMessage) *map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil
	}
	return &m
}

// storedReply is the item of a reply as the run stored it.
type storedReply struct {
	Text           string          `json:"text"`
	Disclosure     string          `json:"disclosure"`
	Components     json.RawMessage `json:"components"`
	InReplyTo      uuid.UUID       `json:"inReplyToMessageId"`
	WindowClosesAt *time.Time      `json:"windowClosesAt"`
}

// storedChange is the item of a change as the run stored it: the operation, its
// arguments and the preview.
type storedChange struct {
	OperationID string          `json:"operationId"`
	Summary     string          `json:"summary"`
	Explain     string          `json:"explain"`
	PathParams  json.RawMessage `json:"pathParams"`
	Query       json.RawMessage `json:"query"`
	Body        json.RawMessage `json:"body"`
}

// seeKeys are the permissions a person needs to see what a role's agent read
// (V20): Communications' conversations-view, and the read permissions of the
// template's allowlist.
func (t *template) seeKeys() []string {
	keys := []string{permViewConversations}
	for _, n := range t.Needs {
		if n.Level == levelRead && !slices.Contains(keys, n.Permission) {
			keys = append(keys, n.Permission)
		}
	}
	return keys
}

// maySee says the caller holds what the templates' agents read, so may see an
// item or a transcript of a role of one of them. Any failure reads as no.
func (s *server) maySee(ctx context.Context, templateKeys ...string) bool {
	var keys []string
	for _, key := range templateKeys {
		tpl := templates[key]
		if tpl == nil {
			return false
		}
		for _, k := range tpl.seeKeys() {
			if !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
		}
	}
	return contracts.HasPermissions(ctx, s.deps.Access, keys...)
}

// allTemplateKeys are the keys of every template of this build.
func allTemplateKeys() []string {
	keys := make([]string, 0, len(templates))
	for k := range templates {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// page reads limit and offset, with the defaults.
func page(limit, offset *int32) (n, off int32) {
	n = defaultPageSize
	if limit != nil && *limit >= 1 {
		n = min(*limit, maxPageSize)
	}
	if offset != nil && *offset > 0 {
		off = *offset
	}
	return n, off
}

var itemStates = []string{itemWaiting, filterDecided, itemSent, itemEditedSent, itemDiscarded, itemWithdrawn, itemExpired}

// ListReviewItems (GET /api/v1/agents/review)
func (s *server) ListReviewItems(ctx context.Context, req gen.ListReviewItemsRequestObject) (gen.ListReviewItemsResponseObject, error) {
	var state *string
	if req.Params.State != nil {
		st := string(*req.Params.State)
		if !slices.Contains(itemStates, st) {
			return invalid("The filter is not valid", map[string][]string{"state": {"state must be one of " + strings.Join(itemStates, ", ")}}), nil
		}
		state = &st
	}
	if !s.maySee(ctx, allTemplateKeys()...) {
		return notPermitted(), nil
	}
	limit, offset := page(req.Params.Limit, req.Params.Offset)
	q := store.New(s.deps.Pool)
	rows, err := q.ListReviewItems(ctx, store.ListReviewItemsParams{AgentID: req.Params.AgentId, State: state, N: limit, Off: offset})
	if err != nil {
		return nil, fmt.Errorf("agents: list the review items: %w", err)
	}
	total, err := q.CountReviewItems(ctx, store.CountReviewItemsParams{AgentID: req.Params.AgentId, State: state})
	if err != nil {
		return nil, fmt.Errorf("agents: count the review items: %w", err)
	}
	out := gen.ReviewItemList{Items: make([]gen.ReviewItem, 0, len(rows)), Total: total, Limit: limit, Offset: offset}
	for _, r := range rows {
		out.Items = append(out.Items, itemView(itemRow(r)))
	}
	return gen.ListReviewItems200JSONResponse(out), nil
}

// GetReviewCount (GET /api/v1/agents/review/count)
func (s *server) GetReviewCount(ctx context.Context, _ gen.GetReviewCountRequestObject) (gen.GetReviewCountResponseObject, error) {
	n, err := store.New(s.deps.Pool).CountWaitingItems(ctx)
	if err != nil {
		return nil, fmt.Errorf("agents: count the waiting items: %w", err)
	}
	return gen.GetReviewCount200JSONResponse(gen.ReviewCount{Waiting: n}), nil
}

// GetReviewItem (GET /api/v1/agents/review/{itemId})
func (s *server) GetReviewItem(ctx context.Context, req gen.GetReviewItemRequestObject) (gen.GetReviewItemResponseObject, error) {
	row, err := store.New(s.deps.Pool).GetReviewItem(ctx, req.ItemId)
	if isNoRows(err) {
		return notFound(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("agents: read the review item: %w", err)
	}
	if !s.maySee(ctx, row.RoleTemplate) {
		return notPermitted(), nil
	}
	return gen.GetReviewItem200JSONResponse(itemView(row)), nil
}

// loadForDecision reads the item a reviewer decides and judges what holds for any
// decision: the item is there, the reviewer may see it, it still waits, and it is
// at the version they read.
func (s *server) loadForDecision(ctx context.Context, itemID uuid.UUID, ifMatch string) (itemRow, *refusal, error) {
	version, ok := parseVersion(ifMatch)
	if !ok {
		rf := badIfMatch()
		return itemRow{}, &rf, nil
	}
	row, err := store.New(s.deps.Pool).GetReviewItem(ctx, itemID)
	if isNoRows(err) {
		rf := notFound()
		return itemRow{}, &rf, nil
	}
	if err != nil {
		return itemRow{}, nil, fmt.Errorf("agents: read the review item: %w", err)
	}
	if !s.maySee(ctx, row.RoleTemplate) {
		rf := notPermitted()
		return itemRow{}, &rf, nil
	}
	if row.State != itemWaiting {
		rf := itemDecided()
		return itemRow{}, &rf, nil
	}
	if row.Version != version {
		rf := versionConflict()
		return itemRow{}, &rf, nil
	}
	return row, nil, nil
}

// reviewOutcome is what a decision comes to, for finish to record.
type reviewOutcome struct {
	state     string // the item's new state
	outcome   string // what Communications answered, or why the item was withdrawn
	item      []byte // the item as sent, when the reviewer edited it
	editRatio *float32
	// handOff, when set, hands the conversation off with this code and summary:
	// withdrawn items, expired ones and discarded replies do (V20).
	handOff *handOffRequest
	// rerun asks for a new run on the conversation: a newer message arrived.
	rerun bool
	// reason and text are a discard's.
	reason, text *string
}

// finish records a decision in one transaction of this module's own rows: the
// item at the version the reviewer read, the day's counts and the hand-off. A
// decision someone else made first is answered as theirs when it is the same
// kind of send (a second approval of one reply is one send), and as a conflict
// otherwise.
func (s *server) finish(ctx context.Context, row itemRow, reviewer uuid.UUID, out reviewOutcome, claimed bool) (gen.ReviewItem, *refusal, error) {
	now := s.deps.Clock()
	var saved store.AgentsReviewItem
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		from := itemWaiting
		if claimed {
			from = itemDeciding
		}
		saved, err = q.DecideReviewItem(ctx, store.DecideReviewItemParams{
			ID: row.ID, Version: row.Version, FromState: from, CheckVersion: !claimed, State: out.state, DecidedBy: &reviewer, Now: now, DiscardReason: out.reason, DiscardText: out.text,
			EditRatio: out.editRatio, Outcome: &out.outcome, Item: out.item,
		})
		if err != nil {
			return err
		}
		usage := store.AddUsageParams{AgentID: row.AgentID, Day: s.day(now)}
		switch out.state {
		case itemSent, itemEditedSent:
			if row.Kind == kindReply {
				usage.Sent = 1
			}
		case itemDiscarded:
			usage.Discarded = 1
		}
		if usage.Sent+usage.Discarded > 0 {
			if err := q.AddUsage(ctx, usage); err != nil {
				return err
			}
		}
		if out.handOff != nil {
			// The note never holds the draft: it links the review item, which is gated.
			summary := out.handOff.summary + " " + reviewLink(row.ID)
			if err := markHandedOff(ctx, q, row.AgentID, row.RoleID, row.ConversationID, out.handOff.code, summary, now); err != nil {
				return err
			}
		}
		if out.rerun {
			return q.UpsertRunRequest(ctx, store.UpsertRunRequestParams{
				RoleID: row.RoleID, ConversationID: row.ConversationID, Reason: reasonMessage, NotBefore: now, DueBy: now.Add(maxWait), Now: now,
			})
		}
		return nil
	})
	if isNoRows(err) {
		// Someone decided, or changed, the item between the read and now.
		current, rerr := store.New(s.deps.Pool).GetReviewItem(ctx, row.ID)
		if rerr != nil {
			return gen.ReviewItem{}, nil, fmt.Errorf("agents: read the review item: %w", rerr)
		}
		if (current.State == itemSent || current.State == itemEditedSent) && (out.state == itemSent || out.state == itemEditedSent) {
			return itemView(current), nil, nil
		}
		rf := versionConflict()
		if current.State != itemWaiting {
			rf = itemDecided()
		}
		return gen.ReviewItem{}, &rf, nil
	}
	if err != nil {
		return gen.ReviewItem{}, nil, fmt.Errorf("agents: record the decision: %w", err)
	}
	return itemView(withNames(row, saved)), nil, nil
}

// withNames is the saved item with the names the row was read with.
func withNames(row itemRow, saved store.AgentsReviewItem) itemRow {
	return itemRow{
		ID: saved.ID, RunID: saved.RunID, AgentID: saved.AgentID, RoleID: saved.RoleID, ConversationID: saved.ConversationID,
		CustomerID: saved.CustomerID, Kind: saved.Kind, Item: saved.Item, Score: saved.Score, Sources: saved.Sources,
		ModelScore: saved.ModelScore, HeldBy: saved.HeldBy, State: saved.State, Version: saved.Version,
		DecidedByUserID: saved.DecidedByUserID, DecidedAt: saved.DecidedAt, DiscardReason: saved.DiscardReason,
		DiscardText: saved.DiscardText, EditRatio: saved.EditRatio, Outcome: saved.Outcome, ExpiresAt: saved.ExpiresAt,
		CreatedAt: saved.CreatedAt, ChannelID: saved.ChannelID, AgentName: row.AgentName, RoleName: row.RoleName, RoleTemplate: row.RoleTemplate,
	}
}

// ApproveReviewItem (POST /api/v1/agents/review/{itemId}/approve)
func (s *server) ApproveReviewItem(ctx context.Context, req gen.ApproveReviewItemRequestObject) (gen.ApproveReviewItemResponseObject, error) {
	row, rf, err := s.loadForDecision(ctx, req.ItemId, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if rf != nil {
		return *rf, nil
	}
	item, rf, err := s.approve(ctx, row, callerID(ctx), nil)
	if err != nil {
		return nil, err
	}
	if rf != nil {
		return *rf, nil
	}
	return gen.ApproveReviewItem200JSONResponse(item), nil
}

// SendEditedReviewItem (POST /api/v1/agents/review/{itemId}/send)
func (s *server) SendEditedReviewItem(ctx context.Context, req gen.SendEditedReviewItemRequestObject) (gen.SendEditedReviewItemResponseObject, error) {
	row, rf, err := s.loadForDecision(ctx, req.ItemId, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if rf != nil {
		return *rf, nil
	}
	if req.Body == nil {
		return invalid("A body is required", nil), nil
	}
	text := strings.TrimSpace(req.Body.Text)
	switch {
	case row.Kind != kindReply:
		return invalid("Only a reply can be edited", map[string][]string{"text": {"a change is approved or discarded as it is"}}), nil
	case text == "":
		return invalid("The text is not valid", map[string][]string{"text": {"text is required"}}), nil
	case utf8.RuneCountInString(text) > maxEditedText:
		return invalid("The text is not valid", map[string][]string{"text": {fmt.Sprintf("text must be at most %d characters", maxEditedText)}}), nil
	}
	edit := &replyEdit{text: text}
	if req.Body.Components != nil {
		raw, _ := json.Marshal(*req.Body.Components)
		edit.components, edit.replaceComponents = raw, true
	}
	item, rf, err := s.approve(ctx, row, callerID(ctx), edit)
	if err != nil {
		return nil, err
	}
	if rf != nil {
		return *rf, nil
	}
	return gen.SendEditedReviewItem200JSONResponse(item), nil
}

// approve runs what the item would do, as the agent, on the reviewer's
// authority, and records the outcome.
func (s *server) approve(ctx context.Context, row itemRow, reviewer uuid.UUID, edit *replyEdit) (gen.ReviewItem, *refusal, error) {
	var (
		out reviewOutcome
		rf  *refusal
		err error
	)
	// Claim the item before any call or send, so two reviewers never both act: the one that
	// loses the claim is told the item was decided (or moved on).
	won, err := store.New(s.deps.Pool).ClaimReviewItem(ctx, store.ClaimReviewItemParams{ID: row.ID, Version: row.Version, Now: s.deps.Clock()})
	if err != nil {
		return gen.ReviewItem{}, nil, fmt.Errorf("agents: claim the review item: %w", err)
	}
	if won == 0 {
		current, rerr := store.New(s.deps.Pool).GetReviewItem(ctx, row.ID)
		if rerr != nil {
			return gen.ReviewItem{}, nil, fmt.Errorf("agents: read the review item: %w", rerr)
		}
		rf := versionConflict()
		if current.State != itemWaiting || current.Version == row.Version {
			rf = itemDecided()
		}
		return gen.ReviewItem{}, &rf, nil
	}
	switch row.Kind {
	case kindReply:
		out, rf, err = s.approveReply(ctx, row, reviewer, edit)
	default:
		out, rf, err = s.approveChange(ctx, row, reviewer)
	}
	if err != nil || rf != nil {
		// Nothing was done: the claim is given back.
		s.release(ctx, row.ID)
		return gen.ReviewItem{}, rf, err
	}
	return s.finish(ctx, row, reviewer, out, true)
}

// release gives a claim back.
func (s *server) release(ctx context.Context, id uuid.UUID) {
	if err := store.New(s.deps.Pool).ReleaseReviewItem(context.WithoutCancel(ctx), id); err != nil {
		s.warn("agents: give a claim back", "item", id, "err", err)
	}
}

// discardReasons are the reasons a reviewer may give (V20).
var discardReasons = []string{"wrong_facts", "wrong_tone", "should_not_answer", "other"}

// discardWords are the reasons in the words the hand-off note gives them.
var discardWords = map[string]string{
	"wrong_facts": "wrong facts", "wrong_tone": "wrong tone", "should_not_answer": "should not answer this", "other": "other",
}

// DiscardReviewItem (POST /api/v1/agents/review/{itemId}/discard)
//
// Discarding a reply hands the conversation off: the people answer it.
func (s *server) DiscardReviewItem(ctx context.Context, req gen.DiscardReviewItemRequestObject) (gen.DiscardReviewItemResponseObject, error) {
	row, rf, err := s.loadForDecision(ctx, req.ItemId, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if rf != nil {
		return *rf, nil
	}
	if req.Body == nil {
		return invalid("A body is required", nil), nil
	}
	reason := string(req.Body.Reason)
	if !slices.Contains(discardReasons, reason) {
		return invalid("The reason is not valid", map[string][]string{"reason": {"reason must be one of " + strings.Join(discardReasons, ", ")}}), nil
	}
	var text *string
	if req.Body.Text != nil {
		t := strings.TrimSpace(*req.Body.Text)
		if utf8.RuneCountInString(t) > maxDiscardText {
			return invalid("The text is not valid", map[string][]string{"text": {fmt.Sprintf("text must be at most %d characters", maxDiscardText)}}), nil
		}
		if t != "" {
			text = &t
		}
	}
	out := reviewOutcome{state: itemDiscarded, outcome: "discarded", reason: &reason, text: text}
	if row.Kind == kindReply {
		out.handOff = &handOffRequest{code: handDiscarded, summary: "A colleague discarded the agent's reply (" + discardWords[reason] + "))."}
	}
	item, rf, err := s.finish(ctx, row, callerID(ctx), out, false)
	if err != nil {
		return nil, err
	}
	if rf != nil {
		return *rf, nil
	}
	return gen.DiscardReviewItem200JSONResponse(item), nil
}

// reviewLink is where a review item is read; a hand-off note points at it instead of quoting the draft.
func reviewLink(id uuid.UUID) string { return "/agents/review/" + id.String() }

// convRef names a conversation an agent's role answers.
type convRef struct{ agent, role, conversation uuid.UUID }

// refsOf maps the rows a withdrawal answers to the conversations they name.
func refsOf[R any](rows []R, ref func(R) convRef) []convRef {
	out := make([]convRef, len(rows))
	for i, r := range rows {
		out[i] = ref(r)
	}
	return out
}

// handOffWithdrawn hands each conversation off once, in the transaction of the
// withdrawal: an item that was withdrawn leaves a conversation nobody is
// answering (V20).
func handOffWithdrawn(ctx context.Context, q *store.Queries, refs []convRef, summary string, now time.Time) error {
	done := map[uuid.UUID]bool{}
	for _, r := range refs {
		if done[r.conversation] {
			continue
		}
		done[r.conversation] = true
		if err := markHandedOff(ctx, q, r.agent, r.role, r.conversation, handWithdrawn, summary, now); err != nil {
			return err
		}
	}
	return nil
}
