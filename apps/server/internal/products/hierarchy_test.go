package products

// Ported from Domain/Products/ProductCategoryHierarchyTests.cs's four
// WouldCreateCycle facts (categories.go's wouldCreateCycle). The test
// class's other two facts, GetSelfAndDescendantIds_ReturnsWholeSubtree and
// _ForLeaf, exercise selfAndDescendantCategoryIDs — Task 11's function
// (products.go), ported in category_hierarchy_test.go, which also shares
// categoryHierarchyTree below.

import "testing"

// categoryHierarchyTree is ProductCategoryHierarchyTests' shared fixture: 1
// (root) -> 2 -> 3, and 4 as a second root.
func categoryHierarchyTree() map[int32]*int32 {
	one, two := int32(1), int32(2)
	return map[int32]*int32{
		1: nil,
		2: &one,
		3: &two,
		4: nil,
	}
}

// Ported from Domain/Products/ProductCategoryHierarchyTests.cs.
// WouldCreateCycle_MovingUnderOwnDescendant_ReturnsTrue.
func TestWouldCreateCycle_MovingUnderOwnDescendant_ReturnsTrue(t *testing.T) {
	t.Parallel()
	tree := categoryHierarchyTree()
	three, two := int32(3), int32(2)
	if !wouldCreateCycle(1, &three, tree) {
		t.Error("wouldCreateCycle(1, 3) = false, want true")
	}
	if !wouldCreateCycle(1, &two, tree) {
		t.Error("wouldCreateCycle(1, 2) = false, want true")
	}
	if !wouldCreateCycle(2, &three, tree) {
		t.Error("wouldCreateCycle(2, 3) = false, want true")
	}
}

// Ported from Domain/Products/ProductCategoryHierarchyTests.cs.
// WouldCreateCycle_MovingUnderItself_ReturnsTrue.
func TestWouldCreateCycle_MovingUnderItself_ReturnsTrue(t *testing.T) {
	t.Parallel()
	tree := categoryHierarchyTree()
	two := int32(2)
	if !wouldCreateCycle(2, &two, tree) {
		t.Error("wouldCreateCycle(2, 2) = false, want true")
	}
}

// Ported from Domain/Products/ProductCategoryHierarchyTests.cs.
// WouldCreateCycle_MovingUnderUnrelatedCategory_ReturnsFalse.
func TestWouldCreateCycle_MovingUnderUnrelatedCategory_ReturnsFalse(t *testing.T) {
	t.Parallel()
	tree := categoryHierarchyTree()
	four, one := int32(4), int32(1)
	if wouldCreateCycle(2, &four, tree) {
		t.Error("wouldCreateCycle(2, 4) = true, want false")
	}
	if wouldCreateCycle(3, &one, tree) {
		t.Error("wouldCreateCycle(3, 1) = true, want false")
	}
}

// Ported from Domain/Products/ProductCategoryHierarchyTests.cs.
// WouldCreateCycle_MovingToRoot_ReturnsFalse.
func TestWouldCreateCycle_MovingToRoot_ReturnsFalse(t *testing.T) {
	t.Parallel()
	tree := categoryHierarchyTree()
	if wouldCreateCycle(3, nil, tree) {
		t.Error("wouldCreateCycle(3, nil) = true, want false")
	}
}
