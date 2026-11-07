package knowledge

// Access (knowledge design K4–K6, K16). An article has a default for everyone
// (internal_permission, null inherits) and explicit members, both inherited
// down the tree: what a person may do is the nearest member row for them on
// the path to the root, else the nearest default — always also bounded by
// knowledge:articles, which the router checks. Both are materialised (K5) in
// article_access_default and article_access, recomputed for the affected
// subtree in the transaction that changes them, under the tree's one
// advisory lock, so reads are a join and never a recursive walk.

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/knowledge/gen"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/store"
)

// The levels of access (K4).
const (
	levelWrite = "write"
	levelRead  = "read"
	levelNone  = "none"
)

// maxDepth is the tree's depth limit (K3): 10 levels.
const maxDepth = 10

// validLevel reports whether l is one of the three levels.
func validLevel(l string) bool { return l == levelWrite || l == levelRead || l == levelNone }

// lockTree takes the tree's lock (K5). Every write that changes the tree or
// its access takes it first and then re-reads what it depends on.
func lockTree(ctx context.Context, q *store.Queries) error {
	if err := q.TreeLock(ctx); err != nil {
		return fmt.Errorf("knowledge: take the tree lock: %w", err)
	}
	return nil
}

// recompute rewrites the materialised access of root's subtree (K5, K16)
// from root's parent's rows, which are outside it. The tree's lock is held.
func recompute(ctx context.Context, q *store.Queries, root uuid.UUID) error {
	if err := q.RecomputeDefaults(ctx, root); err != nil {
		return fmt.Errorf("knowledge: recompute the defaults below %s: %w", root, err)
	}
	if err := q.DeleteSubtreeAccess(ctx, root); err != nil {
		return fmt.Errorf("knowledge: clear the access below %s: %w", root, err)
	}
	if err := q.InsertSubtreeAccess(ctx, root); err != nil {
		return fmt.Errorf("knowledge: recompute the access below %s: %w", root, err)
	}
	return nil
}

// meta is an article's own fields with what the caller may do with it.
type meta = store.ArticleMetaForRow

// loadMeta reads the article for user, answering a bare 404 when it does
// not exist.
func loadMeta(ctx context.Context, q *store.Queries, id, user uuid.UUID) (meta, error) {
	m, err := q.ArticleMetaFor(ctx, store.ArticleMetaForParams{UserID: user, ID: id})
	if noRows(err) {
		return m, notFound()
	}
	if err != nil {
		return m, fmt.Errorf("knowledge: read article %s: %w", id, err)
	}
	return m, nil
}

// readable reports whether the caller may read m: not trashed, and access
// not none (K4, K10).
func readable(m meta) bool { return m.TrashedAt == nil && m.Permission != levelNone }

// isPrivate reports whether m is private (K2, K4): a default of none and one
// writer. knowledge:manage neither opens nor administers it.
func isPrivate(m meta) bool { return m.DefaultPermission == levelNone && m.Writers == 1 }

// administers reports whether the caller may change m's access or restore
// its trash group: a writer, or knowledge:manage on an article that is not
// private (K2, K4).
func administers(m meta, manage bool) bool {
	return m.Permission == levelWrite || (manage && !isPrivate(m))
}

// isWorkspace reports whether everyone may read m (K4).
func isWorkspace(m meta) bool { return m.DefaultPermission != levelNone }

// visibilityChanged reports whether a change moved the article between
// private and the workspace (K4), which needs confirmVisibility.
func visibilityChanged(before, after meta) bool {
	return (isPrivate(before) && isWorkspace(after)) || (isWorkspace(before) && isPrivate(after))
}

// readableMeta loads the article and answers 404 when the caller cannot read
// it (K6).
func readableMeta(ctx context.Context, q *store.Queries, id, user uuid.UUID) (meta, error) {
	m, err := loadMeta(ctx, q, id, user)
	if err != nil {
		return m, err
	}
	if !readable(m) {
		return m, notFound()
	}
	return m, nil
}

// writableMeta loads the article and answers 404 when the caller cannot read
// it, 403 article_read_only when they can only read it (K6).
func writableMeta(ctx context.Context, q *store.Queries, id, user uuid.UUID) (meta, error) {
	m, err := readableMeta(ctx, q, id, user)
	if err != nil {
		return m, err
	}
	if m.Permission != levelWrite {
		return m, readOnly()
	}
	return m, nil
}

// sectionOf is the sidebar section an article sits in for the caller (K4):
// workspace when its default is not none, private when the caller is its only
// writer, shared otherwise.
func sectionOf(defaultPermission, permission string, writers int64) gen.KnowledgeSection {
	switch {
	case defaultPermission != levelNone:
		return gen.KnowledgeSectionWorkspace
	case permission == levelWrite && writers == 1:
		return gen.KnowledgeSectionPrivate
	default:
		return gen.KnowledgeSectionShared
	}
}

// permissionOf is the caller's access as the contract names it; an article
// they cannot read is never answered, so none is not among them.
func permissionOf(p string) gen.KnowledgePermission {
	if p == levelWrite {
		return gen.KnowledgePermissionWrite
	}
	return gen.KnowledgePermissionRead
}

// paths are the paths of every article in ids for user (K12): the ancestors
// from the top down, ending below the first the user cannot read and, for
// the Assistant, below the first hidden from it.
func paths(ctx context.Context, q *store.Queries, user uuid.UUID, assistant bool, ids []uuid.UUID) (map[uuid.UUID][]gen.KnowledgePathItem, error) {
	out := map[uuid.UUID][]gen.KnowledgePathItem{}
	for _, id := range ids {
		out[id] = []gen.KnowledgePathItem{}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Ancestors(ctx, store.AncestorsParams{UserID: user, Ids: ids})
	if err != nil {
		return nil, fmt.Errorf("knowledge: read the paths: %w", err)
	}
	cut := map[uuid.UUID]bool{}
	for _, r := range rows { // nearest first, per origin
		if cut[r.Origin] {
			continue
		}
		if !r.Readable || (assistant && r.Hidden) {
			cut[r.Origin] = true
			continue
		}
		out[r.Origin] = append(out[r.Origin], gen.KnowledgePathItem{Id: r.ID, Title: r.Title})
	}
	for id := range out {
		slices.Reverse(out[id])
	}
	return out, nil
}

// checkWriters refuses (400 last_writer) a change that leaves an article in
// root's subtree with its own default other than write without a writer.
// The access is recomputed.
func checkWriters(ctx context.Context, q *store.Queries, root uuid.UUID) error {
	orphaned, err := q.SubtreeWithoutWriter(ctx, root)
	if err != nil {
		return fmt.Errorf("knowledge: check the writers below %s: %w", root, err)
	}
	if orphaned {
		return invalid("members", codeLastWriter, "An article below would be left without a writer.")
	}
	return nil
}

// GetKnowledgeArticleAccess answers an article's default and members, with
// what is inherited (K4). A reader sees it; so does knowledge:manage on an
// article that is not private.
func (s *server) GetKnowledgeArticleAccess(ctx context.Context, req gen.GetKnowledgeArticleAccessRequestObject) (gen.GetKnowledgeArticleAccessResponseObject, error) {
	manage := s.canManage(ctx)
	out, err := s.accessOf(ctx, req.Id, callerID(ctx), manage)
	if err != nil {
		return answer(ctx, s, err, answers[gen.GetKnowledgeArticleAccessResponseObject]{
			notFound: func() gen.GetKnowledgeArticleAccessResponseObject { return gen.GetKnowledgeArticleAccess404Response{} },
		})
	}
	return gen.GetKnowledgeArticleAccess200JSONResponse(out), nil
}

// accessView is what accessOf reads in its snapshot.
type accessView struct {
	m       meta
	path    []store.PathOfRow
	members []store.EffectiveMembersRow
	onPath  []store.MembersOnPathRow
}

// accessOf reads the article's access for user: 404 unless they can read it
// or manage it (K2, K6).
func (s *server) accessOf(ctx context.Context, id, user uuid.UUID, manage bool) (gen.KnowledgeAccess, error) {
	var v accessView
	err := s.withReadTx(ctx, func(q *store.Queries) error {
		m, err := loadMeta(ctx, q, id, user)
		if err != nil {
			return err
		}
		if manages := manage && m.TrashedAt == nil && !isPrivate(m); !readable(m) && !manages {
			return notFound()
		}
		v.m = m
		if v.path, err = q.PathOf(ctx, store.PathOfParams{UserID: user, ID: id}); err != nil {
			return fmt.Errorf("knowledge: read the path of %s: %w", id, err)
		}
		if v.members, err = q.EffectiveMembers(ctx, id); err != nil {
			return fmt.Errorf("knowledge: read the members of %s: %w", id, err)
		}
		if v.onPath, err = q.MembersOnPath(ctx, id); err != nil {
			return fmt.Errorf("knowledge: read the member rows above %s: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return gen.KnowledgeAccess{}, err
	}
	userIDs := make([]*uuid.UUID, 0, len(v.members))
	for i := range v.members {
		userIDs = append(userIDs, &v.members[i].UserID)
	}
	names, err := s.actors(ctx, userIDs...)
	if err != nil {
		return gen.KnowledgeAccess{}, err
	}

	byDepth := map[int32]store.PathOfRow{}
	for _, p := range v.path {
		byDepth[p.Depth] = p
	}
	item := func(depth int32) *gen.KnowledgePathItem {
		p, ok := byDepth[depth]
		if !ok || !p.Readable {
			return nil
		}
		return &gen.KnowledgePathItem{Id: p.ID, Title: p.Title}
	}
	out := gen.KnowledgeAccess{
		ArticleId:           id,
		Section:             sectionOf(v.m.DefaultPermission, v.m.Permission, v.m.Writers),
		EffectivePermission: gen.KnowledgeAccessLevel(v.m.Permission),
		CanManage:           administers(v.m, manage),
		Members:             []gen.KnowledgeMember{},
	}
	if v.m.InternalPermission != nil {
		l := gen.KnowledgeAccessLevel(*v.m.InternalPermission)
		out.InternalPermission = &l
	} else {
		for _, p := range v.path {
			if p.Depth > 0 && p.InternalPermission != "" {
				out.InheritedFrom = item(p.Depth)
				break
			}
		}
	}
	nearest := map[uuid.UUID]int32{}
	for _, r := range v.onPath { // nearest first
		if _, ok := nearest[r.UserID]; !ok {
			nearest[r.UserID] = r.Depth
		}
	}
	for _, mem := range v.members {
		m := gen.KnowledgeMember{UserId: mem.UserID, Permission: gen.KnowledgeAccessLevel(mem.Permission)}
		if name, ok := names[mem.UserID]; ok {
			m.DisplayName = &name
		}
		if depth, ok := nearest[mem.UserID]; ok && depth > 0 {
			m.Inherited = true
			m.InheritedFrom = item(depth)
		}
		out.Members = append(out.Members, m)
	}
	return out, nil
}

// UpdateKnowledgeArticleAccess replaces an article's own default and its own
// member rows (K4) and recomputes its subtree's access in the same
// transaction (K5). A writer may; so may knowledge:manage on an article that
// is not private. The last writer cannot be removed, and a change between
// private and the workspace needs confirmVisibility.
func (s *server) UpdateKnowledgeArticleAccess(ctx context.Context, req gen.UpdateKnowledgeArticleAccessRequestObject) (gen.UpdateKnowledgeArticleAccessResponseObject, error) {
	fail := func(err error) (gen.UpdateKnowledgeArticleAccessResponseObject, error) {
		return answer(ctx, s, err, answers[gen.UpdateKnowledgeArticleAccessResponseObject]{
			badRequest: func(p gen.KnowledgeProblem) gen.UpdateKnowledgeArticleAccessResponseObject {
				return gen.UpdateKnowledgeArticleAccess400ApplicationProblemPlusJSONResponse(p)
			},
			forbidden: func(p gen.KnowledgeProblem) gen.UpdateKnowledgeArticleAccessResponseObject {
				return gen.UpdateKnowledgeArticleAccess403ApplicationProblemPlusJSONResponse(p)
			},
			notFound: func() gen.UpdateKnowledgeArticleAccessResponseObject {
				return gen.UpdateKnowledgeArticleAccess404Response{}
			},
			conflict: func(p gen.KnowledgeProblem) gen.UpdateKnowledgeArticleAccessResponseObject {
				return gen.UpdateKnowledgeArticleAccess409ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	body := req.Body
	if body == nil {
		return fail(invalid("body", "", "A body is required."))
	}
	var f fieldErrors
	if body.InternalPermission != nil && !validLevel(string(*body.InternalPermission)) {
		f.add("internalPermission", "Must be write, read or none.")
	}
	if len(body.Members) > 500 {
		f.add("members", "At most 500 members.")
	}
	seen := map[uuid.UUID]bool{}
	ids := make([]*uuid.UUID, 0, len(body.Members))
	for i, m := range body.Members {
		if !validLevel(string(m.Permission)) {
			f.add("members", fmt.Sprintf("Member %d: the permission must be write, read or none.", i+1))
		}
		if seen[m.UserId] {
			f.add("members", fmt.Sprintf("Member %d: the person is named twice.", i+1))
		}
		seen[m.UserId] = true
		ids = append(ids, &body.Members[i].UserId)
	}
	if err := f.err(); err != nil {
		return fail(err)
	}
	// Every member must be a user the directory knows; asked before the
	// transaction.
	known, err := s.actors(ctx, ids...)
	if err != nil {
		return nil, err
	}
	for i, m := range body.Members {
		if _, ok := known[m.UserId]; !ok {
			f.add("members", fmt.Sprintf("Member %d: no such user.", i+1))
		}
	}
	if err := f.err(); err != nil {
		return fail(err)
	}
	manage := s.canManage(ctx)
	user := callerID(ctx)
	confirm := body.ConfirmVisibility != nil && *body.ConfirmVisibility

	var final meta
	err = s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		if err := lockTree(ctx, q); err != nil {
			return err
		}
		before, err := loadMeta(ctx, q, req.Id, user)
		if err != nil {
			return err
		}
		if before.TrashedAt != nil {
			return notFound()
		}
		if !administers(before, manage) {
			if readable(before) {
				return readOnly()
			}
			return notFound()
		}
		if before.ParentID == nil && body.InternalPermission == nil {
			return invalid("internalPermission", codeRootPermissionRequired, "A root article needs its own default.")
		}
		var own *string
		if body.InternalPermission != nil {
			l := string(*body.InternalPermission)
			own = &l
		}
		if err := q.SetInternalPermission(ctx, store.SetInternalPermissionParams{ID: req.Id, InternalPermission: own}); err != nil {
			return fmt.Errorf("knowledge: set the default of %s: %w", req.Id, err)
		}
		if err := q.DeleteMembers(ctx, req.Id); err != nil {
			return fmt.Errorf("knowledge: clear the members of %s: %w", req.Id, err)
		}
		for _, m := range body.Members {
			if err := q.UpsertMember(ctx, store.UpsertMemberParams{ArticleID: req.Id, UserID: m.UserId, Permission: string(m.Permission)}); err != nil {
				return fmt.Errorf("knowledge: add a member to %s: %w", req.Id, err)
			}
		}
		if err := recompute(ctx, q, req.Id); err != nil {
			return err
		}
		after, err := loadMeta(ctx, q, req.Id, user)
		if err != nil {
			return err
		}
		if after.DefaultPermission != levelWrite && after.Writers == 0 {
			return invalid("members", codeLastWriter, "The article must keep at least one writer.")
		}
		if err := checkWriters(ctx, q, req.Id); err != nil {
			return err
		}
		final = after
		if visibilityChanged(before, after) && !confirm {
			return visibilityChange()
		}
		if err := notifyArticle(ctx, tx, req.Id, eventMetaChanged); err != nil {
			return err
		}
		return notifyTree(ctx, tx)
	})
	if err != nil {
		return fail(err)
	}
	out, err := s.accessOf(ctx, req.Id, user, manage)
	if isNotFound(err) {
		// The change is committed, but it took the article out of the
		// caller's sight — a manager who made it private, a writer who left
		// it: the answer says what they may still know, and no members.
		out = gen.KnowledgeAccess{
			ArticleId:           req.Id,
			Section:             sectionOf(final.DefaultPermission, final.Permission, final.Writers),
			InternalPermission:  body.InternalPermission,
			EffectivePermission: gen.KnowledgeAccessLevel(final.Permission),
			Members:             []gen.KnowledgeMember{},
		}
	} else if err != nil {
		return fail(err)
	}
	return gen.UpdateKnowledgeArticleAccess200JSONResponse(out), nil
}
