namespace Vantigo.Energy.Endpoints.Consumption;

internal static class ConsumptionAggregateValidation
{
    internal static bool TryValidate(
        DateTimeOffset? from,
        DateTimeOffset? to,
        string? resolution,
        out string normalizedResolution,
        out Dictionary<string, string[]> errors)
    {
        normalizedResolution = resolution?.ToLowerInvariant() ?? string.Empty;
        errors = [];

        if (from is null) errors["from"] = ["The from field is required."];
        if (to is null) errors["to"] = ["The to field is required."];
        if (from is not null && to is not null && from >= to)
            errors["to"] = ["The to field must be later than from."];
        if (normalizedResolution is not ("hour" or "day" or "month"))
            errors["resolution"] = ["Resolution must be one of: hour, day, month."];

        return errors.Count == 0;
    }
}