package knowledge

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/knowledge/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// server implements gen.StrictServerInterface, the module's contract
// operations (knowledge design K3–K16). Every operation answers 501 until
// delivery K-A's server lane implements it; the lane moves each into the file
// of its area (articles, access, versions, trash, favourites, search).
type server struct {
	deps module.Deps
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d.
func newServer(d module.Deps) *server { return &server{deps: d} }

// CreateKnowledgeArticle is not implemented yet.
func (s *server) CreateKnowledgeArticle(_ context.Context, _ gen.CreateKnowledgeArticleRequestObject) (gen.CreateKnowledgeArticleResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetKnowledgeArticle is not implemented yet.
func (s *server) GetKnowledgeArticle(_ context.Context, _ gen.GetKnowledgeArticleRequestObject) (gen.GetKnowledgeArticleResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// UpdateKnowledgeArticle is not implemented yet.
func (s *server) UpdateKnowledgeArticle(_ context.Context, _ gen.UpdateKnowledgeArticleRequestObject) (gen.UpdateKnowledgeArticleResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetKnowledgeArticleAccess is not implemented yet.
func (s *server) GetKnowledgeArticleAccess(_ context.Context, _ gen.GetKnowledgeArticleAccessRequestObject) (gen.GetKnowledgeArticleAccessResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// UpdateKnowledgeArticleAccess is not implemented yet.
func (s *server) UpdateKnowledgeArticleAccess(_ context.Context, _ gen.UpdateKnowledgeArticleAccessRequestObject) (gen.UpdateKnowledgeArticleAccessResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// UpdateKnowledgeArticleContent is not implemented yet.
func (s *server) UpdateKnowledgeArticleContent(_ context.Context, _ gen.UpdateKnowledgeArticleContentRequestObject) (gen.UpdateKnowledgeArticleContentResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// MoveKnowledgeArticle is not implemented yet.
func (s *server) MoveKnowledgeArticle(_ context.Context, _ gen.MoveKnowledgeArticleRequestObject) (gen.MoveKnowledgeArticleResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// RestoreKnowledgeArticle is not implemented yet.
func (s *server) RestoreKnowledgeArticle(_ context.Context, _ gen.RestoreKnowledgeArticleRequestObject) (gen.RestoreKnowledgeArticleResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetKnowledgeArticleText is not implemented yet.
func (s *server) GetKnowledgeArticleText(_ context.Context, _ gen.GetKnowledgeArticleTextRequestObject) (gen.GetKnowledgeArticleTextResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// TrashKnowledgeArticle is not implemented yet.
func (s *server) TrashKnowledgeArticle(_ context.Context, _ gen.TrashKnowledgeArticleRequestObject) (gen.TrashKnowledgeArticleResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// ListKnowledgeArticleVersions is not implemented yet.
func (s *server) ListKnowledgeArticleVersions(_ context.Context, _ gen.ListKnowledgeArticleVersionsRequestObject) (gen.ListKnowledgeArticleVersionsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetKnowledgeArticleVersion is not implemented yet.
func (s *server) GetKnowledgeArticleVersion(_ context.Context, _ gen.GetKnowledgeArticleVersionRequestObject) (gen.GetKnowledgeArticleVersionResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// RestoreKnowledgeArticleVersion is not implemented yet.
func (s *server) RestoreKnowledgeArticleVersion(_ context.Context, _ gen.RestoreKnowledgeArticleVersionRequestObject) (gen.RestoreKnowledgeArticleVersionResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// ListKnowledgeFavourites is not implemented yet.
func (s *server) ListKnowledgeFavourites(_ context.Context, _ gen.ListKnowledgeFavouritesRequestObject) (gen.ListKnowledgeFavouritesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// RemoveKnowledgeFavourite is not implemented yet.
func (s *server) RemoveKnowledgeFavourite(_ context.Context, _ gen.RemoveKnowledgeFavouriteRequestObject) (gen.RemoveKnowledgeFavouriteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// AddKnowledgeFavourite is not implemented yet.
func (s *server) AddKnowledgeFavourite(_ context.Context, _ gen.AddKnowledgeFavouriteRequestObject) (gen.AddKnowledgeFavouriteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// MoveKnowledgeFavourite is not implemented yet.
func (s *server) MoveKnowledgeFavourite(_ context.Context, _ gen.MoveKnowledgeFavouriteRequestObject) (gen.MoveKnowledgeFavouriteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// SearchKnowledge is not implemented yet.
func (s *server) SearchKnowledge(_ context.Context, _ gen.SearchKnowledgeRequestObject) (gen.SearchKnowledgeResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// EmptyKnowledgeTrash is not implemented yet.
func (s *server) EmptyKnowledgeTrash(_ context.Context, _ gen.EmptyKnowledgeTrashRequestObject) (gen.EmptyKnowledgeTrashResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// ListKnowledgeTrash is not implemented yet.
func (s *server) ListKnowledgeTrash(_ context.Context, _ gen.ListKnowledgeTrashRequestObject) (gen.ListKnowledgeTrashResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// ListKnowledgeTree is not implemented yet.
func (s *server) ListKnowledgeTree(_ context.Context, _ gen.ListKnowledgeTreeRequestObject) (gen.ListKnowledgeTreeResponseObject, error) {
	return nil, module.ErrNotImplemented
}
