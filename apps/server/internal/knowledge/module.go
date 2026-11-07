// Package knowledge is the Knowledge module (nb Kunnskap): the company's own
// articles — routines, policies and how-tos — as pages in a tree, with access
// inherited down it, autosaved ProseMirror content and its history, the trash,
// favourites, full-text search and the live topics of an open article and of
// the sidebar (knowledge design,
// docs/superpowers/specs/2026-11-07-knowledge-and-live-design.md §3). It
// requires no other module (K1).
package knowledge

import (
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// The two permission keys (K2). knowledge:articles opens the module, and
// each article's own access (K4) decides what its holder may do with it;
// knowledge:manage administers every article that is not private, and never
// opens a private one.
const (
	permArticles = "knowledge:articles"
	permManage   = "knowledge:manage"
)

var permissions = []contracts.Permission{
	{
		Key: permArticles, Display: "Use Knowledge",
		Description: "See the Knowledge app, and read and write articles as each article's access allows.",
		Category:    "Knowledge", Sensitive: false, Delegable: true,
	},
	{
		Key: permManage, Display: "Administer Knowledge",
		Description: "See and change the access of every article that is not private, restore anyone's trashed article and empty the trash. Private articles stay private.",
		Category:    "Knowledge", Sensitive: false, Delegable: false,
	},
}

// limits are the module's rate limits; none beyond the platform's.
var limits = map[string]ratelimit.Policy{}

// documentBodyBytes is the request-body cap of the operations that carry a
// document: above doc.MaxBytes and its envelope, so a document over 2 MiB
// reaches the handler and is answered 413 document_too_large (K8) rather than
// cut off by the router.
const documentBodyBytes int64 = 3 << 20

var bodyLimits = map[string]int64{
	"createKnowledgeArticle":        documentBodyBytes,
	"updateKnowledgeArticleContent": documentBodyBytes,
}

// Module is the Knowledge module.
func Module() module.Module {
	return module.Module{
		Name:        "knowledge",
		Permissions: permissions,
		Mount:       mount,
		Workers:     workers,
		LiveTopics:  liveTopics,
	}
}

// mount builds the module's handler.
func mount(d module.Deps) (http.Handler, error) {
	router := module.NewRouter(module.RouterOptions{
		Doc: d.Doc, Access: d.Access, Limiter: d.Limiter, Limits: limits, Catalog: d.Catalog,
		BodyLimits: bodyLimits,
	})
	strict := gen.NewStrictHandlerWithOptions(newServer(d), nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  module.DecodeError(apicommon.WriteDecodeError),
		ResponseErrorHandlerFunc: module.ResponseError(),
	})
	handler := gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseRouter:       router,
		ErrorHandlerFunc: module.DecodeError(apicommon.WriteDecodeError),
	})
	if err := router.Err(); err != nil {
		return nil, err
	}
	return handler, nil
}
