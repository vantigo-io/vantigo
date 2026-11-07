package knowledge

// Favourites (knowledge design K11): personal, ordered by drag. One the
// person can no longer read is not shown, and knowledge.retention removes it.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/knowledge/gen"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/store"
)

// ListKnowledgeFavourites answers the caller's favourites in their order.
func (s *server) ListKnowledgeFavourites(ctx context.Context, _ gen.ListKnowledgeFavouritesRequestObject) (gen.ListKnowledgeFavouritesResponseObject, error) {
	var rows []store.ListFavouritesRow
	err := s.withReadTx(ctx, func(q *store.Queries) error {
		var err error
		rows, err = q.ListFavourites(ctx, callerID(ctx))
		if err != nil {
			return fmt.Errorf("knowledge: read the favourites: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	items := make([]gen.KnowledgeTreeNode, 0, len(rows))
	for _, r := range rows {
		parent := r.ParentID
		if !r.ParentReadable {
			parent = nil
		}
		items = append(items, treeNode(r.ID, parent, r.Position, r.Title, r.Icon, r.Locked, r.DefaultPermission, r.Permission, r.Writers, r.HasChildren))
	}
	return gen.ListKnowledgeFavourites200JSONResponse{Items: items}, nil
}

// AddKnowledgeFavourite puts a readable article last among the caller's
// favourites; one already there stays where it is.
func (s *server) AddKnowledgeFavourite(ctx context.Context, req gen.AddKnowledgeFavouriteRequestObject) (gen.AddKnowledgeFavouriteResponseObject, error) {
	user := callerID(ctx)
	now := s.deps.Clock()
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		if _, err := readableMeta(ctx, q, req.Id, user); err != nil {
			return err
		}
		pos, err := q.FavouritePosition(ctx, store.FavouritePositionParams{UserID: user, ArticleID: req.Id})
		if err != nil {
			return fmt.Errorf("knowledge: read a favourite: %w", err)
		}
		if pos != "" {
			return nil
		}
		last, err := q.LastFavouritePosition(ctx, store.LastFavouritePositionParams{UserID: user, Exclude: req.Id})
		if err != nil {
			return fmt.Errorf("knowledge: read the last favourite: %w", err)
		}
		key, err := keyBetween(last, "")
		if err != nil {
			return fmt.Errorf("knowledge: a key after %q: %w", last, err)
		}
		if err := q.InsertFavourite(ctx, store.InsertFavouriteParams{UserID: user, ArticleID: req.Id, Position: key, Now: now}); err != nil {
			return fmt.Errorf("knowledge: add a favourite: %w", err)
		}
		return nil
	})
	if err != nil {
		return answer(ctx, s, err, answers[gen.AddKnowledgeFavouriteResponseObject]{
			notFound: func() gen.AddKnowledgeFavouriteResponseObject { return gen.AddKnowledgeFavourite404Response{} },
		})
	}
	return gen.AddKnowledgeFavourite204Response{}, nil
}

// RemoveKnowledgeFavourite removes one of the caller's favourites.
func (s *server) RemoveKnowledgeFavourite(ctx context.Context, req gen.RemoveKnowledgeFavouriteRequestObject) (gen.RemoveKnowledgeFavouriteResponseObject, error) {
	n, err := store.New(s.deps.Pool).DeleteFavourite(ctx, store.DeleteFavouriteParams{UserID: callerID(ctx), ArticleID: req.Id})
	if err != nil {
		return nil, fmt.Errorf("knowledge: remove a favourite: %w", err)
	}
	if n == 0 {
		return gen.RemoveKnowledgeFavourite404Response{}, nil
	}
	return gen.RemoveKnowledgeFavourite204Response{}, nil
}

// MoveKnowledgeFavourite puts one of the caller's favourites after another
// (first when afterId is null).
func (s *server) MoveKnowledgeFavourite(ctx context.Context, req gen.MoveKnowledgeFavouriteRequestObject) (gen.MoveKnowledgeFavouriteResponseObject, error) {
	user := callerID(ctx)
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		pos, err := q.FavouritePosition(ctx, store.FavouritePositionParams{UserID: user, ArticleID: req.Id})
		if err != nil {
			return fmt.Errorf("knowledge: read a favourite: %w", err)
		}
		if pos == "" {
			return notFound()
		}
		prev := ""
		if req.Body != nil && req.Body.AfterId != nil {
			if *req.Body.AfterId == req.Id {
				return invalid("afterId", codeAfterInvalid, "A favourite cannot come after itself.")
			}
			if prev, err = q.FavouritePosition(ctx, store.FavouritePositionParams{UserID: user, ArticleID: *req.Body.AfterId}); err != nil {
				return fmt.Errorf("knowledge: read a favourite: %w", err)
			}
			if prev == "" {
				return invalid("afterId", codeAfterInvalid, "afterId is not among your favourites.")
			}
		}
		next, err := q.NextFavouritePosition(ctx, store.NextFavouritePositionParams{UserID: user, Exclude: req.Id, After: prev})
		if err != nil {
			return fmt.Errorf("knowledge: read the next favourite: %w", err)
		}
		key, err := keyBetween(prev, next)
		if err != nil {
			return fmt.Errorf("knowledge: a key between %q and %q: %w", prev, next, err)
		}
		if err := q.MoveFavourite(ctx, store.MoveFavouriteParams{UserID: user, ArticleID: req.Id, Position: key}); err != nil {
			return fmt.Errorf("knowledge: move a favourite: %w", err)
		}
		return nil
	})
	if err != nil {
		return answer(ctx, s, err, answers[gen.MoveKnowledgeFavouriteResponseObject]{
			badRequest: func(p gen.KnowledgeProblem) gen.MoveKnowledgeFavouriteResponseObject {
				return gen.MoveKnowledgeFavourite400ApplicationProblemPlusJSONResponse(p)
			},
			notFound: func() gen.MoveKnowledgeFavouriteResponseObject { return gen.MoveKnowledgeFavourite404Response{} },
		})
	}
	return gen.MoveKnowledgeFavourite204Response{}, nil
}
