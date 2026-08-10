using Vantigo.Energy.Domain.SupplyPeriods;

namespace Vantigo.Energy.Endpoints.SupplyPeriods.Dtos;

internal sealed record SupplyPeriodResponse(
    int Id,
    int MeteringPointId,
    int CustomerId,
    DateTimeOffset Start,
    DateTimeOffset? End,
    string Status)
{
    internal static SupplyPeriodResponse FromDomain(SupplyPeriod period) => new(
        period.Id, period.MeteringPointId, period.CustomerId, period.Start, period.End, period.Status.ToString());
}

internal sealed record CreateSupplyPeriodRequest(int CustomerId, DateTimeOffset Start);
internal sealed record EndSupplyPeriodRequest(DateTimeOffset End);
internal sealed record SwitchSupplyPeriodRequest(int CustomerId, DateTimeOffset SwitchAt);
internal sealed record SwitchSupplyPeriodResponse(SupplyPeriodResponse? EndedPeriod, SupplyPeriodResponse NewPeriod);