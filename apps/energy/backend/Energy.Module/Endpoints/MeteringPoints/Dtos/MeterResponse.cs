namespace Vantigo.Energy.Endpoints.MeteringPoints.Dtos;

internal sealed record MeterResponse(int Id, int MeteringPointId, string MeterNumber, DateTimeOffset InstalledAt, DateTimeOffset? RemovedAt)
{
    internal static MeterResponse FromDomain(Domain.Meters.Meter meter) =>
        new(meter.Id, meter.MeteringPointId, meter.MeterNumber, meter.InstalledAt, meter.RemovedAt);
}

internal sealed record ReplaceMeterRequest(string? MeterNumber, DateTimeOffset? InstalledAt)
{
    internal Dictionary<string, string[]> Validate()
    {
        var errors = new Dictionary<string, string[]>(StringComparer.Ordinal);
        if (Domain.Meters.Meter.ValidateMeterNumber(MeterNumber) is { } meterError) errors["meterNumber"] = [meterError];
        if (InstalledAt is null) errors["installedAt"] = ["Installed at is required."];
        return errors;
    }
}