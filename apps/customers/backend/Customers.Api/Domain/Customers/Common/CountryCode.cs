using Vantigo.Customers.Api.Domain.Exceptions;

namespace Vantigo.Customers.Api.Domain.Customers.Common;

public readonly record struct CountryCode
{
    private readonly string _value;

    public CountryCode(string value)
    {
        if (Validate(value) is { } error)
        {
            throw new DomainException(error);
        }

        _value = value.Trim().ToLower();
    }

    /// <summary>
    /// Attempts to create a country code from the given value, returning a
    /// human-readable error instead of throwing when the value is invalid.
    /// </summary>
    public static bool TryCreate(string? value, out CountryCode result, out string? error)
    {
        error = Validate(value);
        result = error is null ? new CountryCode(value!) : default;
        return error is null;
    }

    private static string? Validate(string? value) =>
        string.IsNullOrWhiteSpace(value)
            ? "A country code cannot be null or empty"
            : null;

    public static implicit operator CountryCode(string value) => new (value);
    public static implicit operator string(CountryCode countryCode) => countryCode._value;

    /// <summary>
    /// Converts the country code to its persisted representation.
    /// </summary>
    public string ToPersistence() => _value;

    /// <summary>
    /// Recreates a country code from its persisted representation.
    /// </summary>
    public static CountryCode FromPersistence(string value) => new (value);
}
