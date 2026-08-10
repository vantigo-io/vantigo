namespace Vantigo.Energy.Domain.MeteringPoints;

public static class MarketTimeZone
{
    public const string Oslo = "Europe/Oslo";
    public const string Stockholm = "Europe/Stockholm";
    public const string Copenhagen = "Europe/Copenhagen";
    public const string Helsinki = "Europe/Helsinki";

    public static string GetId(string? priceArea)
    {
        var prefix = priceArea is { Length: >= 2 } ? priceArea[..2] : string.Empty;
        return prefix switch
        {
            "SE" => Stockholm,
            "DK" => Copenhagen,
            "FI" => Helsinki,
            _ => Oslo,
        };
    }
}