using Vantigo.Products.Domain.Products;

namespace Vantigo.Products.Module.Tests.Domain.Products;

public sealed class ProductCategoryHierarchyTests
{
    // A small tree: 1 (root) -> 2 -> 3, and 4 as a second root.
    private static readonly Dictionary<int, int?> Tree = new()
    {
        [1] = null,
        [2] = 1,
        [3] = 2,
        [4] = null,
    };

    [Fact]
    public void WouldCreateCycle_MovingUnderOwnDescendant_ReturnsTrue()
    {
        Assert.True(ProductCategoryHierarchy.WouldCreateCycle(1, 3, Tree));
        Assert.True(ProductCategoryHierarchy.WouldCreateCycle(1, 2, Tree));
        Assert.True(ProductCategoryHierarchy.WouldCreateCycle(2, 3, Tree));
    }

    [Fact]
    public void WouldCreateCycle_MovingUnderItself_ReturnsTrue()
    {
        Assert.True(ProductCategoryHierarchy.WouldCreateCycle(2, 2, Tree));
    }

    [Fact]
    public void WouldCreateCycle_MovingUnderUnrelatedCategory_ReturnsFalse()
    {
        Assert.False(ProductCategoryHierarchy.WouldCreateCycle(2, 4, Tree));
        Assert.False(ProductCategoryHierarchy.WouldCreateCycle(3, 1, Tree));
    }

    [Fact]
    public void WouldCreateCycle_MovingToRoot_ReturnsFalse()
    {
        Assert.False(ProductCategoryHierarchy.WouldCreateCycle(3, null, Tree));
    }

    [Fact]
    public void GetSelfAndDescendantIds_ReturnsWholeSubtree()
    {
        var ids = ProductCategoryHierarchy.GetSelfAndDescendantIds(1, Tree);

        Assert.Equal([1, 2, 3], ids.OrderBy(id => id));
    }

    [Fact]
    public void GetSelfAndDescendantIds_ForLeaf_ReturnsOnlySelf()
    {
        Assert.Equal([3], ProductCategoryHierarchy.GetSelfAndDescendantIds(3, Tree));
        Assert.Equal([4], ProductCategoryHierarchy.GetSelfAndDescendantIds(4, Tree));
    }
}