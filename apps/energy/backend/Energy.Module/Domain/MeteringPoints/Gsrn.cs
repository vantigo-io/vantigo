using Vantigo.Energy.Domain.Exceptions;

namespace Vantigo.Energy.Domain.MeteringPoints;

/// <summary>A GS1 Global Service Relation Number, represented without separators.</summary>
public readonly record struct Gsrn
{
    public const int Length = 18;
    private readonly string _value;

    public Gsrn(string value)
    {
        if (!IsValid(value))
            throw new DomainException("A GSRN must contain exactly 18 digits.");
        _value = value;
    }

    public string Value => _value;

    public static bool IsValid(string? value) =>
        value is { Length: Length } && value.All(character => character is >= '0' and <= '9');

    public static bool TryCreate(string? value, out Gsrn result, out string? error)
    {
        if (!IsValid(value))
        {
            result = default;
            error = "A GSRN must contain exactly 18 digits.";
            return false;
        }

        result = new Gsrn(value!);
        error = null;
        return true;
    }

    public static implicit operator string(Gsrn gsrn) => gsrn._value;
    public override string ToString() => _value;
}