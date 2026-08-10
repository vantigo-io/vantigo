namespace Vantigo.Energy.Endpoints.Consumption.Dtos;

internal sealed record ConsumptionAggregateResponse(
    DateTimeOffset BucketStart,
    DateTimeOffset BucketEnd,
    decimal QuantityKwh,
    long IntervalCount,
    bool HasEstimated);

internal sealed record CustomerConsumptionAggregateResponse(
    int MeteringPointId,
    DateTimeOffset BucketStart,
    DateTimeOffset BucketEnd,
    decimal QuantityKwh,
    long IntervalCount,
    bool HasEstimated);