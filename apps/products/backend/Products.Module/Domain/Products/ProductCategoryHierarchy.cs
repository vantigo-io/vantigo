namespace Vantigo.Products.Domain.Products;

/// <summary>
/// Hierarchy rules for the category adjacency list. Categories are few enough that
/// checks operate on the full id → parent-id map loaded from the database.
/// </summary>
public static class ProductCategoryHierarchy
{
    /// <summary>
    /// Returns whether re-parenting <paramref name="categoryId"/> under
    /// <paramref name="newParentId"/> would create a cycle, determined by walking up
    /// the ancestor chain from the new parent.
    /// </summary>
    public static bool WouldCreateCycle(
        int categoryId,
        int? newParentId,
        IReadOnlyDictionary<int, int?> parentByCategoryId)
    {
        var current = newParentId;
        while (current is { } ancestorId)
        {
            if (ancestorId == categoryId)
            {
                return true;
            }

            current = parentByCategoryId.GetValueOrDefault(ancestorId);
        }

        return false;
    }

    /// <summary>
    /// Returns the ids of <paramref name="categoryId"/> and all of its descendants,
    /// used to filter products by a category subtree.
    /// </summary>
    public static IReadOnlyList<int> GetSelfAndDescendantIds(
        int categoryId,
        IReadOnlyDictionary<int, int?> parentByCategoryId)
    {
        var childrenByParentId = parentByCategoryId
            .Where(pair => pair.Value is not null)
            .ToLookup(pair => pair.Value!.Value, pair => pair.Key);

        var result = new List<int>();
        var pending = new Stack<int>();
        pending.Push(categoryId);

        while (pending.Count > 0)
        {
            var current = pending.Pop();
            result.Add(current);
            foreach (var child in childrenByParentId[current])
            {
                pending.Push(child);
            }
        }

        return result;
    }
}