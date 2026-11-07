package knowledge

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/store"
	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The live topics (knowledge design K13). Events carry identifiers only: a
// screen that hears one refetches. The server lane notifies them with
// live.Notify on the transaction that writes (live design L6).
const (
	// liveArticle is one article's topic, keyed by its id, for its readers:
	// content.changed, meta.changed (title, icon, lock, access) and trashed.
	// Presence is allowed on it: viewing and editing (K9).
	liveArticle = "knowledge.article"
	// liveTree is the sidebar's topic, for anyone with knowledge:articles:
	// tree.changed, always without an id, since an id would tell everyone
	// that a hidden article exists. It is sent only when something the
	// sidebar shows changes — never for a content save that keeps the title.
	liveTree = "knowledge.tree"
)

// liveTopics is the module's LiveTopics. The tree is authorised by
// knowledge:articles; an article by the rule its GET applies — the
// permission, and read access to an article that is not trashed (K4–K6). An
// article that does not exist and one the person may not read are both
// ErrForbidden, so a subscription is no existence oracle.
func liveTopics(d module.Deps) []live.Topic {
	return []live.Topic{
		{Kind: liveTree, Authorize: func(ctx context.Context, key string) error {
			if key != "" || !live.HasPermission(ctx, permArticles) {
				return live.ErrForbidden
			}
			return nil
		}},
		{Kind: liveArticle, Presence: true, Authorize: func(ctx context.Context, key string) error {
			id, err := uuid.Parse(key)
			if err != nil || !live.HasPermission(ctx, permArticles) {
				return live.ErrForbidden
			}
			userID, ok := contracts.AsUserFrom(ctx)
			if !ok {
				return live.ErrForbidden
			}
			readable, err := store.New(d.Pool).CanReadArticle(ctx, store.CanReadArticleParams{UserID: userID, ArticleID: id})
			if err != nil {
				return fmt.Errorf("knowledge: authorise an article's topic: %w", err)
			}
			if !readable {
				return live.ErrForbidden
			}
			return nil
		}},
	}
}
