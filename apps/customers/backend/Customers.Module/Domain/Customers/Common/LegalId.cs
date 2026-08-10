using Vantigo.Customers.Domain.Exceptions;

namespace Vantigo.Customers.Domain.Customers.Common;

public readonly record struct LegalId
{
    private readonly string _value;

    public const int MaxLength = 50;

    public LegalId(string value)
    {
        if (Validate(value) is { } error)
        {
            throw new DomainException(error);
        }

        _value = value.Trim().ToLower();
    }

    /// <summary>
    /// Attempts to create a legal id from the given value, returning a
    /// human-readable error instead of throwing when the value is invalid.
    /// </summary>
    public static bool TryCreate(string? value, out LegalId result, out string? error)
    {
        error = Validate(value);
        result = error is null ? new LegalId(value!) : default;
        return error is null;
    }

    private static string? Validate(string? value) => value switch
    {
        _ when string.IsNullOrWhiteSpace(value) => "A legal id cannot be null or empty",
        { Length: > MaxLength } => $"A legal id cannot be longer than {MaxLength} characters, the given value was {value.Length} characters",
        _ => null,
    };

    public static implicit operator LegalId(string value) => new(value);
    public static implicit operator string(LegalId legalId) => legalId._value;

    /// <summary>
    /// Converts the legal id to its persisted representation.
    /// </summary>
    public string ToPersistence() => _value;

    /// <summary>
    /// Recreates a legal id from its persisted representation.
    /// </summary>
    public static LegalId FromPersistence(string value) => new(value);
}