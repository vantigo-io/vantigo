package knowledge

// The trash (knowledge design K10): an article goes with its subtree as one
// trash group, invisible everywhere but Trash, restored whole, and deleted for
// good after 30 days by knowledge.retention or by knowledge:manage's Empty
// trash.

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/knowledge/gen"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/store"
)

// trashRetention is how long a trash group is kept (K10).
const trashRetention = 30 * 24 * time.Hour

// trashNotifyCap is the largest trash group whose every article is told
// trashed on its own topic (K13); a larger one tells its root only.
const trashNotifyCap = 200

// TrashKnowledgeArticle trashes the article and its subtree in one
// transaction, as one trash group.
func (s *server) TrashKnowledgeArticle(ctx context.Context, req gen.TrashKnowledgeArticleRequestObject) (gen.TrashKnowledgeArticleResponseObject, error) {
	user := callerID(ctx)
	now := s.deps.Clock()
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		if err := lockTree(ctx, q); err != nil {
			return err
		}
		if _, err := writableMeta(ctx, q, req.Id, user); err != nil {
			return err
		}
		trashed, err := q.TrashSubtree(ctx, store.TrashSubtreeParams{ID: req.Id, UserID: user, Now: now})
		if err != nil {
			return fmt.Errorf("knowledge: trash article %s: %w", req.Id, err)
		}
		// Every article that went is told on its own topic, up to a cap; a
		// larger group tells only its root, and its other readers learn it
		// from tree.changed and their next fetch.
		if len(trashed) > trashNotifyCap {
			trashed = []uuid.UUID{req.Id}
		}
		for _, id := range trashed {
			if err := notifyArticle(ctx, tx, id, eventTrashed); err != nil {
				return err
			}
		}
		return notifyTree(ctx, tx)
	})
	if err != nil {
		return answer(ctx, s, err, answers[gen.TrashKnowledgeArticleResponseObject]{
			forbidden: func(p gen.KnowledgeProblem) gen.TrashKnowledgeArticleResponseObject {
				return gen.TrashKnowledgeArticle403ApplicationProblemPlusJSONResponse(p)
			},
			notFound: func() gen.TrashKnowledgeArticleResponseObject { return gen.TrashKnowledgeArticle404Response{} },
		})
	}
	return gen.TrashKnowledgeArticle204Response{}, nil
}

// RestoreKnowledgeArticle brings back the article's whole trash group (K10,
// §9: a child cannot leave its group alone) under its old parent, or to the
// root of its old section when that parent is trashed itself. The caller
// must be able to write the group's root; knowledge:manage restores any
// group that is not private. Anyone else is answered 404, as if it did not
// exist.
func (s *server) RestoreKnowledgeArticle(ctx context.Context, req gen.RestoreKnowledgeArticleRequestObject) (gen.RestoreKnowledgeArticleResponseObject, error) {
	manage := s.canManage(ctx)
	user := callerID(ctx)
	var out gen.KnowledgeRestored
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		if err := lockTree(ctx, q); err != nil {
			return err
		}
		m, err := loadMeta(ctx, q, req.Id, user)
		if err != nil {
			return err
		}
		if m.TrashedAt == nil {
			if readable(m) {
				return conflict(codeNotTrashed, "The article is not in the trash", "")
			}
			return notFound()
		}
		root := *m.TrashRoot
		rm, err := loadMeta(ctx, q, root, user)
		if err != nil {
			return err
		}
		if !administers(rm, manage) {
			return notFound()
		}
		parentTrashed, err := q.ParentTrashed(ctx, root)
		if err != nil {
			return fmt.Errorf("knowledge: read the parent of %s: %w", root, err)
		}
		moved := rm.ParentID != nil && parentTrashed
		if moved {
			// To the root of its old section, keeping who can read it.
			if err := q.CopyAccessToMembers(ctx, root); err != nil {
				return fmt.Errorf("knowledge: keep the members of %s: %w", root, err)
			}
			position, err := lastPosition(ctx, q, nil, root)
			if err != nil {
				return err
			}
			own := rm.DefaultPermission
			if err := q.MoveArticle(ctx, store.MoveArticleParams{ID: root, ParentID: nil, Position: position, InternalPermission: &own}); err != nil {
				return fmt.Errorf("knowledge: move article %s to the root: %w", root, err)
			}
		}
		n, err := q.RestoreGroup(ctx, root)
		if err != nil {
			return fmt.Errorf("knowledge: restore the trash group %s: %w", root, err)
		}
		if moved {
			if err := recompute(ctx, q, root); err != nil {
				return err
			}
		}
		out = gen.KnowledgeRestored{Id: root, RestoredCount: int32(n), GroupRestored: root != req.Id, MovedToRoot: moved}
		if err := notifyArticle(ctx, tx, root, eventMetaChanged); err != nil {
			return err
		}
		return notifyTree(ctx, tx)
	})
	if err != nil {
		return answer(ctx, s, err, answers[gen.RestoreKnowledgeArticleResponseObject]{
			notFound: func() gen.RestoreKnowledgeArticleResponseObject { return gen.RestoreKnowledgeArticle404Response{} },
			conflict: func(p gen.KnowledgeProblem) gen.RestoreKnowledgeArticleResponseObject {
				return gen.RestoreKnowledgeArticle409ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	return gen.RestoreKnowledgeArticle200JSONResponse(out), nil
}

// ListKnowledgeTrash answers the trash groups whose root the caller could
// write, and with knowledge:manage every one that is not private, newest
// first.
func (s *server) ListKnowledgeTrash(ctx context.Context, _ gen.ListKnowledgeTrashRequestObject) (gen.ListKnowledgeTrashResponseObject, error) {
	manage := s.canManage(ctx)
	user := callerID(ctx)
	var rows []store.ListTrashRow
	err := s.withReadTx(ctx, func(q *store.Queries) error {
		var err error
		rows, err = q.ListTrash(ctx, store.ListTrashParams{UserID: user, Manage: manage})
		if err != nil {
			return fmt.Errorf("knowledge: read the trash: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	ids := make([]*uuid.UUID, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].TrashedBy)
	}
	names, err := s.actors(ctx, ids...)
	if err != nil {
		return nil, err
	}
	items := make([]gen.KnowledgeTrashItem, 0, len(rows))
	for _, r := range rows {
		var trashedAt time.Time
		if r.TrashedAt != nil {
			trashedAt = *r.TrashedAt
		}
		item := gen.KnowledgeTrashItem{
			Id: r.ID, Title: r.Title, Icon: r.Icon, ArticleCount: max(r.ArticleCount, 1),
			TrashedAt: trashedAt, TrashedBy: actorOf(names, r.TrashedBy), PurgeAt: trashedAt.Add(trashRetention),
		}
		if r.ParentReadable && r.ParentID != nil && r.ParentTitle != nil {
			item.Parent = &gen.KnowledgePathItem{Id: *r.ParentID, Title: *r.ParentTitle}
		}
		items = append(items, item)
	}
	return gen.ListKnowledgeTrash200JSONResponse{Items: items}, nil
}

// EmptyKnowledgeTrash deletes every trashed article for good, with its
// versions (K10). The router has checked knowledge:manage.
func (s *server) EmptyKnowledgeTrash(ctx context.Context, _ gen.EmptyKnowledgeTrashRequestObject) (gen.EmptyKnowledgeTrashResponseObject, error) {
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		if err := lockTree(ctx, q); err != nil {
			return err
		}
		if _, err := q.EmptyTrash(ctx); err != nil {
			return fmt.Errorf("knowledge: empty the trash: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.EmptyKnowledgeTrash204Response{}, nil
}
