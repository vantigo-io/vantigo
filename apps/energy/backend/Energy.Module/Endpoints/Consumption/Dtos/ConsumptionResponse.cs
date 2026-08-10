using Vantigo.Energy.Domain.Consumption;

namespace Vantigo.Energy.Endpoints.Consumption.Dtos;

internal sealed record ConsumptionResponse(
    long Id,
    int MeteringPointId,
    DateTimeOffset Start,
    DateTimeOffset End,
    decimal QuantityKwh,
    string Quality,
    string Source,
    DateTimeOffset ReceivedAt)
{
    internal static ConsumptionResponse FromDomain(ConsumptionInterval interval) => new(
        interval.Id, interval.MeteringPointId, interval.Start, interval.End, interval.QuantityKwh,
        interval.Quality.ToString(), interval.Source.ToString(), interval.ReceivedAt);
}

internal sealed record ManualConsumptionRequest(DateTimeOffset Start, DateTimeOffset End, decimal QuantityKwh);