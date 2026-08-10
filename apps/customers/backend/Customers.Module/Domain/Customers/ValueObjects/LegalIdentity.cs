using Vantigo.Customers.Domain.Customers.Common;

namespace Vantigo.Customers.Domain.Customers.ValueObjects;

public readonly record struct LegalIdentity
{
    public required CountryCode Country { get; init; }
    public required LegalType Type { get; init; }
    public required LegalId Id { get; init; }
    public required LegalName Name { get; init; }
    public required LegalSource Source { get; init; }

    /// <summary>
    /// Attempts to create a legal identity from the given raw values. When one or more
    /// values are invalid, all validation errors are returned at once, keyed by the
    /// field name ("country", "type", "id", "name" and "source").
    /// </summary>
    public static bool TryCreate(
        string? country,
        string? type,
        string? id,
        string? name,
        string? source,
        out LegalIdentity identity,
        out IReadOnlyDictionary<string, string[]> errors)
    {
        var validationErrors = new Dictionary<string, string[]>();

        if (!CountryCode.TryCreate(country, out var countryCode, out var countryError))
        {
            validationErrors["country"] = [countryError!];
        }

        if (!LegalType.TryCreate(type, out var legalType, out var typeError))
        {
            validationErrors["type"] = [typeError!];
        }

        if (!LegalId.TryCreate(id, out var legalId, out var idError))
        {
            validationErrors["id"] = [idError!];
        }

        if (!LegalName.TryCreate(name, out var legalName, out var nameError))
        {
            validationErrors["name"] = [nameError!];
        }

        if (!LegalSource.TryCreate(source, out var legalSource, out var sourceError))
        {
            validationErrors["source"] = [sourceError!];
        }

        errors = validationErrors;

        if (validationErrors.Count > 0)
        {
            identity = default;
            return false;
        }

        identity = new LegalIdentity
        {
            Country = countryCode,
            Type = legalType,
            Id = legalId,
            Name = legalName,
            Source = legalSource,
        };

        return true;
    }
}