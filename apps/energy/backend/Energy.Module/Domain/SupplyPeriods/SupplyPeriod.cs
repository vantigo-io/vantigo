using Vantigo.Energy.Domain.Exceptions;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Energy.Domain.SupplyPeriods;

public sealed class SupplyPeriod : ITenantOwned
{
    public Guid TenantId { get; set; }
    public int Id { get; set; }
    public int MeteringPointId { get; set; }
    public int CustomerId { get; set; }
    public DateTimeOffset Start { get; set; }
    public DateTimeOffset? End { get; set; }
    public SupplyPeriodStatus Status { get; set; } = SupplyPeriodStatus.Active;
    public MeteringPoints.MeteringPoint? MeteringPoint { get; set; }

    public static bool Overlaps(DateTimeOffset firstStart, DateTimeOffset? firstEnd,
        DateTimeOffset secondStart, DateTimeOffset? secondEnd) =>
        firstStart < (secondEnd ?? DateTimeOffset.MaxValue) && secondStart < (firstEnd ?? DateTimeOffset.MaxValue);

    public bool Overlaps(DateTimeOffset start, DateTimeOffset? end) => Overlaps(Start, End, start, end);

    public static string? Validate(DateTimeOffset start, DateTimeOffset? end)
    {
        if (start.Offset != TimeSpan.Zero || (end is not null && end.Value.Offset != TimeSpan.Zero)) return "Start and end must be UTC timestamps.";
        if (end is not null && end <= start) return "End must be later than start.";
        return null;
    }

    public void Validate()
    {
        if (CustomerId <= 0) throw new DomainException("Customer ID must be greater than zero.");
        if (Validate(Start, End) is { } error) throw new DomainException(error);
    }
}