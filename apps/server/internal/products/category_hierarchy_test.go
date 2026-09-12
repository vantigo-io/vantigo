package products

// Ported from Domain/Products/ProductCategoryHierarchyTests.cs's remaining
// two facts, GetSelfAndDescendantIds_ReturnsWholeSubtree and
// GetSelfAndDescendantIds_ForLeaf_ReturnsOnlySelf: hierarchy_test.go (Task
// 12) ports the class's four WouldCreateCycle facts against categories.go's
// wouldCreateCycle and notes these final two exercise
// selfAndDescendantCategoryIDs instead — Task 11's own function
// (products.go), not Task 12's. products_test.go's
// TestGetProducts_FiltersByCategoryIncludingDescendants already exercises
// it end-to-end through getProducts, but only a two-level tree and only the
// "has descendants" shape; it never pins a three-level subtree or the
// "childless category returns only itself" case directly, so it is not a
// substitute for these two — hence this file.

import (
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/products/store"
)

// categoryHierarchyRows adapts hierarchy_test.go's categoryHierarchyTree
// fixture (1 (root) -> 2 -> 3, and 4 as a second root) to the
// []store.ListCategoryParentsRow shape selfAndDescendantCategoryIDs takes.
func categoryHierarchyRows() []store.ListCategoryParentsRow {
	tree := categoryHierarchyTree()
	ids := make([]int32, 0, len(tree))
	for id := range tree {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	rows := make([]store.ListCategoryParentsRow, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, store.ListCategoryParentsRow{ID: id, ParentID: tree[id]})
	}
	return rows
}

// Ported from Domain/Products/ProductCategoryHierarchyTests.cs.
// GetSelfAndDescendantIds_ReturnsWholeSubtree.
func TestSelfAndDescendantCategoryIDs_ReturnsWholeSubtree(t *testing.T) {
	t.Parallel()
	ids := selfAndDescendantCategoryIDs(1, categoryHierarchyRows())
	slices.Sort(ids)
	want := []int32{1, 2, 3}
	if !slices.Equal(ids, want) {
		t.Errorf("selfAndDescendantCategoryIDs(1, ...) = %v (sorted), want %v", ids, want)
	}
}

// Ported from Domain/Products/ProductCategoryHierarchyTests.cs.
// GetSelfAndDescendantIds_ForLeaf_ReturnsOnlySelf: a leaf (3, under 2) and a
// second, childless root (4) both return only themselves.
func TestSelfAndDescendantCategoryIDs_ForLeafOrChildlessRoot_ReturnsOnlySelf(t *testing.T) {
	t.Parallel()
	rows := categoryHierarchyRows()

	if ids := selfAndDescendantCategoryIDs(3, rows); !slices.Equal(ids, []int32{3}) {
		t.Errorf("selfAndDescendantCategoryIDs(3, ...) = %v, want [3]", ids)
	}
	if ids := selfAndDescendantCategoryIDs(4, rows); !slices.Equal(ids, []int32{4}) {
		t.Errorf("selfAndDescendantCategoryIDs(4, ...) = %v, want [4]", ids)
	}
}
