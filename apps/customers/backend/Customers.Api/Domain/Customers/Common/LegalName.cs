using Vantigo.Customers.Api.Domain.Exceptions;

namespace Vantigo.Customers.Api.Domain.Customers.Common;

public readonly record struct LegalName
{
    private readonly string _value;

    public const int MaxLength = 255;

    public LegalName(string value)
    {
        if (Validate(value) is { } error)
        {
            throw new DomainException(error);
        }

        _value = value.Trim();
    }

    /// <summary>
    /// Attempts to create a legal name from the given value, returning a
    /// human-readable error instead of throwing when the value is invalid.
    /// </summary>
    public static bool TryCreate(string? value, out LegalName result, out string? error)
    {
        error = Validate(value);
        result = error is null ? new LegalName(value!) : default;
        return error is null;
    }

    private static string? Validate(string? value) => value switch
    {
        _ when string.IsNullOrWhiteSpace(value) => "A legal name cannot be null or empty",
        { Length: > MaxLength } => $"A legal name cannot be longer than {MaxLength} characters, the given value was {value.Length} characters",
        _ => null,
    };

    public static implicit operator LegalName(string value) => new (value);
    public static implicit operator string(LegalName legalName) => legalName._value;

    /// <summary>
    /// Converts the legal name to its persisted representation.
    /// </summary>
    public string ToPersistence() => _value;

    /// <summary>
    /// Recreates a legal name from its persisted representation.
    /// </summary>
    public static LegalName FromPersistence(string value) => new (value);
}
