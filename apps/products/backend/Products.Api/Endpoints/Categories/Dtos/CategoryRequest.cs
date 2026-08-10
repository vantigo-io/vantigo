using Vantigo.Products.Api.Domain.Products;

namespace Vantigo.Products.Api.Endpoints.Categories.Dtos;

/// <summary>
/// The category fields supplied by API clients when creating or updating a category.
/// </summary>
internal readonly record struct CategoryRequest
{
    public required string Name { get; init; }
    public int? ParentId { get; init; }

    internal Dictionary<string, string[]> Validate()
    {
        var errors = new Dictionary<string, string[]>();

        if (string.IsNullOrWhiteSpace(Name))
        {
            errors["name"] = ["'name' is required."];
        }
        else if (Name.Trim().Length > ProductCategory.NameMaxLength)
        {
            errors["name"] = [$"'name' must be at most {ProductCategory.NameMaxLength} characters."];
        }

        return errors;
    }
}