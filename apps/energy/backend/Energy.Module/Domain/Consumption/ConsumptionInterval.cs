using Vantigo.Energy.Domain.Exceptions;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Energy.Domain.Consumption;

public sealed class ConsumptionInterval : ITenantOwned
{
    public Guid TenantId { get; set; }
    public long Id { get; set; }
    public int MeteringPointId { get; set; }
    public DateTimeOffset Start { get; set; }
    public DateTimeOffset End { get; set; }
    public decimal QuantityKwh { get; set; }
    public ConsumptionQuality Quality { get; set; }
    public ConsumptionSource Source { get; set; }
    public DateTimeOffset ReceivedAt { get; set; }
    public bool IsCurrent { get; set; } = true;
    public long? SupersedesId { get; set; }
    public DateTimeOffset? SupersedesStart { get; set; }
    public ConsumptionInterval? Supersedes { get; set; }
    public MeteringPoints.MeteringPoint? MeteringPoint { get; set; }

    public static string? Validate(DateTimeOffset start, DateTimeOffset end, decimal quantityKwh)
    {
        if (start.Offset != TimeSpan.Zero || end.Offset != TimeSpan.Zero) return "Start and end must be UTC timestamps.";
        if (end <= start) return "End must be later than start.";
        if (quantityKwh < 0) return "Quantity must be zero or greater.";
        return null;
    }

    public void Validate()
    {
        if (Validate(Start, End, QuantityKwh) is { } error) throw new DomainException(error);
    }
}