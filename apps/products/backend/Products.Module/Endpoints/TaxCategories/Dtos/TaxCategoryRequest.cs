using Vantigo.Products.Domain.Products;

namespace Vantigo.Products.Endpoints.TaxCategories.Dtos;

/// <summary>The tax category fields supplied by API clients.</summary>
internal readonly record struct TaxCategoryRequest
{
    public required string Name { get; init; }
    public required string Kind { get; init; }
    public required decimal Rate { get; init; }

    internal Dictionary<string, string[]> Validate()
    {
        var errors = new Dictionary<string, string[]>();

        if (string.IsNullOrWhiteSpace(Name))
        {
            errors["name"] = ["'name' is required."];
        }
        else if (Name.Trim().Length > TaxCategory.NameMaxLength)
        {
            errors["name"] = [$"'name' must be at most {TaxCategory.NameMaxLength} characters."];
        }

        if (!Enum.TryParse<TaxCategoryKind>(Kind, ignoreCase: true, out _))
        {
            errors["kind"] = [$"'kind' must be one of 'Standard', 'Reduced', 'Zero' or 'Exempt', but was '{Kind}'."];
        }

        if (Rate is < 0 or > 1)
        {
            errors["rate"] = [$"'rate' must be between 0 and 1, but was {Rate}."];
        }

        return errors;
    }

    internal TaxCategory ToDomain() => new()
    {
        Name = Name.Trim(),
        Kind = Enum.Parse<TaxCategoryKind>(Kind, ignoreCase: true),
        Rate = Rate,
    };
}