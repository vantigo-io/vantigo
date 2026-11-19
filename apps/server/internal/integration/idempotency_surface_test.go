package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The Idempotency-Key in the integrator's document (idempotency design I14,
// I25) over the whole composed contract: what `vantigo openapi integrator`
// writes offers the header on exactly the operations the router honours it
// on, and merges the middleware's 409 and 422 into each operation's own
// without losing a word or a schema of them.

const idempotencyParameterRef = "#/components/parameters/IdempotencyKey"

func refName(ref string) string { return ref[strings.LastIndex(ref, "/")+1:] }

func TestIdempotency_TheDocumentOffersTheKeyWhereTheRouterHonoursIt(t *testing.T) {
	body, err := module.IntegratorDocument(append([]module.Module{identity.Module(nil)}, everyModule()...)...)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Errorf("the document does not validate: %v", err)
	}
	posts, catalog := composedPOSTs(t)

	kept := map[string]*openapi3.Operation{}
	offered := map[string]bool{}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			kept[op.OperationID] = op
			for _, p := range op.Parameters {
				if p.Ref == idempotencyParameterRef {
					if method != http.MethodPost {
						t.Errorf("%s %s (%s) offers the key", method, path, op.OperationID)
					}
					offered[op.OperationID] = true
				}
			}
		}
	}

	var diff []string
	inScope := 0
	for id, op := range posts {
		honoured := module.Idempotent(http.MethodPost, op, catalog)
		if honoured {
			inScope++
			if kept[id] == nil {
				// Build keeps every operation a key could call, and the
				// predicate admits only those: none is dropped.
				diff = append(diff, id+" is in scope but not in the document")
			}
		}
		if honoured != offered[id] && kept[id] != nil {
			diff = append(diff, fmt.Sprintf("%s: the router honours the key %v, the document offers it %v", id, honoured, offered[id]))
		}
	}
	sort.Strings(diff)
	if len(diff) > 0 {
		t.Errorf("the document and the router disagree:\n%s", strings.Join(diff, "\n"))
	}
	if len(offered) != inScope || inScope < 300 {
		t.Errorf("%d operations offer the key, %d are in scope", len(offered), inScope)
	}

	// Merged, never replaced.
	merged, created := 0, 0
	for id := range offered {
		original, got := posts[id], kept[id]
		for code, component := range map[string]string{"409": "IdempotencyConflict", "422": "IdempotencyKeyReused"} {
			mine, theirs := got.Responses.Value(code), original.Responses.Value(code)
			if mine == nil || mine.Value == nil || mine.Value.Description == nil {
				t.Errorf("%s has no %s", id, code)
				continue
			}
			if theirs == nil {
				created++
				if mine.Ref != "#/components/responses/"+component {
					t.Errorf("%s's %s is %q, want the shared %s", id, code, mine.Ref, component)
				}
				continue
			}
			merged++
			description := *mine.Value.Description
			if want := strings.TrimRight(*theirs.Value.Description, " \n"); !strings.HasPrefix(description, want) ||
				!strings.Contains(description, "With an Idempotency-Key, ") {
				t.Errorf("%s's %s lost its own text: %q", id, code, description)
			}
			for contentType, media := range theirs.Value.Content {
				gotMedia := mine.Value.Content[contentType]
				if gotMedia == nil || gotMedia.Schema == nil {
					t.Errorf("%s's %s lost its %s", id, code, contentType)
					continue
				}
				if media.Schema == nil || media.Schema.Ref == "" {
					continue
				}
				schema := gotMedia.Schema
				if schema.Ref == "" && schema.Value != nil && len(schema.Value.OneOf) == 2 {
					schema = schema.Value.OneOf[0]
				}
				// The combined contract names common.yaml's schemas common_*.
				if got, want := refName(schema.Ref), refName(media.Schema.Ref); got != want && got != "common_"+want {
					t.Errorf("%s's %s %s is %q, want %q kept", id, code, contentType, schema.Ref, media.Schema.Ref)
				}
			}
			if mine.Value.Content["application/json"] == nil {
				t.Errorf("%s's %s does not offer the JSON the middleware answers", id, code)
			}
		}
	}
	t.Logf("%d operations offer the key: %d answers merged into the operation's own, %d of their own", len(offered), merged, created)
	if merged == 0 {
		t.Error("no operation documents a 409 or 422 of its own: the merge is not exercised")
	}

	// An operation with a key of its own keeps it, and its answers, as they are.
	native := kept["postInvoicesByIdApplyCredit"]
	if native == nil {
		t.Fatal("postInvoicesByIdApplyCredit is not in the document")
	}
	for _, p := range native.Parameters {
		if p.Ref == idempotencyParameterRef {
			t.Error("postInvoicesByIdApplyCredit is offered the general key beside its own")
		}
	}
	if d := *native.Responses.Value("409").Value.Description; strings.Contains(d, "With an Idempotency-Key") {
		t.Errorf("postInvoicesByIdApplyCredit's 409 was merged: %q", d)
	}
}
