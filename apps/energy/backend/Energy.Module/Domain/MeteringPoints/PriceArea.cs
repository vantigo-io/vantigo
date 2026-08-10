namespace Vantigo.Energy.Domain.MeteringPoints;

public static class PriceArea
{
    public static bool IsValid(string? value) =>
        value is { Length: >= 3 and <= 4 } &&
        char.IsLetter(value[0]) && char.IsLetter(value[1]) &&
        value[0] is >= 'A' and <= 'Z' && value[1] is >= 'A' and <= 'Z' &&
        value[2..].All(character => character is >= '0' and <= '9');
}