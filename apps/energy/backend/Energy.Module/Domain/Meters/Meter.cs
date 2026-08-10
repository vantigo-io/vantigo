using Vantigo.Energy.Domain.Exceptions;

namespace Vantigo.Energy.Domain.Meters;

public sealed class Meter
{
    public int Id { get; set; }
    public int MeteringPointId { get; set; }
    public string MeterNumber { get; set; } = string.Empty;
    public DateTimeOffset InstalledAt { get; set; }
    public DateTimeOffset? RemovedAt { get; set; }
    public MeteringPoints.MeteringPoint? MeteringPoint { get; set; }

    public static string? ValidateMeterNumber(string? value) => string.IsNullOrWhiteSpace(value)
        ? "Meter number is required."
        : value.Trim().Length > 64 ? "Meter number cannot be longer than 64 characters." : null;

    public void Validate()
    {
        if (ValidateMeterNumber(MeterNumber) is { } meterError) throw new DomainException(meterError);
    }
}