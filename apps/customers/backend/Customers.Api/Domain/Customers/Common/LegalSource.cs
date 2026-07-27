using Vantigo.Customers.Api.Domain.Exceptions;

namespace Vantigo.Customers.Api.Domain.Customers.Common;

/// <summary>
/// Identifies where the legal identity data of a customer was retrieved from, so
/// consumers can judge the trustworthiness of the data. Data picked from a public
/// registry (e.g. Brønnøysundregisteret) keeps its registry source only as long as
/// it is unmodified; manually entered or edited data is marked as manual.
/// </summary>
public readonly record struct LegalSource
{
    private readonly string _value;

    public const string Brreg = "brreg";
    public const string Manual = "manual";

    private static readonly string[] ValidValues = [Brreg, Manual];

    public LegalSource(string value)
    {
        if (Validate(value) is { } error)
        {
            throw new DomainException(error);
        }

        _value = value.Trim().ToLower();
    }

    /// <summary>
    /// Attempts to create a legal source from the given value, returning a
    /// human-readable error instead of throwing when the value is invalid.
    /// </summary>
    public static bool TryCreate(string? value, out LegalSource result, out string? error)
    {
        error = Validate(value);
        result = error is null ? new LegalSource(value!) : default;
        return error is null;
    }

    private static string? Validate(string? value) => value switch
    {
        _ when string.IsNullOrWhiteSpace(value) => "A legal source cannot be null or empty",
        _ when !ValidValues.Contains(value.Trim().ToLower()) =>
            $"A legal source must be one of '{string.Join("', '", ValidValues)}', but was '{value}'",
        _ => null,
    };

    public static implicit operator LegalSource(string value) => new(value);
    public static implicit operator string(LegalSource legalSource) => legalSource._value;

    /// <summary>
    /// Converts the legal source to its persisted representation.
    /// </summary>
    public string ToPersistence() => _value;

    /// <summary>
    /// Recreates a legal source from its persisted representation.
    /// </summary>
    public static LegalSource FromPersistence(string value) => new(value);
}