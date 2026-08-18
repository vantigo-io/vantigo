using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Contracts.AspNetCore.Authorization;
using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Domain.SupplyPeriods;

namespace Vantigo.Energy.Endpoints;

internal static class EnergyStatsEndpoints
{
    private const int DefaultPeriodDays = 30;

    internal static void MapEnergyStatsEndpoints(this IEndpointRouteBuilder api)
    {
        api.MapGet("/stats/summary", Summary)
            .WithSummary("Get energy dashboard summary")
            .Produces<SummaryResponse>()
            .ProducesProblem(StatusCodes.Status400BadRequest)
            .RequirePermission("energy:metering-points-view")
            .RequirePermission("energy:meters-view")
            .RequirePermission("energy:supply-periods-view")
            .RequirePermission("energy:consumption-view");
        api.MapGet("/stats/timeseries", Timeseries)
            .WithSummary("Get energy dashboard time series")
            .Produces<IReadOnlyList<DailyBucket>>()
            .ProducesProblem(StatusCodes.Status400BadRequest)
            .RequirePermission("energy:consumption-view");
        api.MapGet("/stats/attention", Attention)
            .WithSummary("Get energy dashboard attention items")
            .Produces<IReadOnlyList<AttentionItem>>()
            .RequirePermission("energy:supply-periods-view");
    }

    private static async Task<Results<Ok<SummaryResponse>, ProblemHttpResult>> Summary(
        DateTimeOffset? from,
        DateTimeOffset? to,
        EnergyDbContext db,
        CancellationToken cancellationToken)
    {
        if (!TryNormalizePeriod(from, to, out var period, out var problem)) return problem;

        var previous = period.Previous;
        var meteringPoints = db.MeteringPoints.AsNoTracking();
        var supplyPeriods = db.SupplyPeriods.AsNoTracking();
        var consumption = db.ConsumptionIntervals.AsNoTracking().Where(item => item.IsCurrent);

        var meteringPointCount = await meteringPoints.CountAsync(cancellationToken);
        var meteringPointCountAtPeriodStart = await meteringPoints.CountAsync(
            item => item.CreatedAt < period.From, cancellationToken);
        var activeSupplyPeriods = await supplyPeriods.CountAsync(
            item => item.Status == SupplyPeriodStatus.Active, cancellationToken);
        var activeSupplyPeriodsAtPeriodStart = await supplyPeriods.CountAsync(
            item => item.Status == SupplyPeriodStatus.Active && item.Start < period.From &&
                    (item.End == null || item.End >= period.From), cancellationToken);
        var consumptionKwh = await consumption
            .Where(item => item.Start >= period.From && item.End <= period.To)
            .Select(item => (decimal?)item.QuantityKwh)
            .SumAsync(cancellationToken) ?? 0m;
        var previousConsumptionKwh = await consumption
            .Where(item => item.Start >= previous.From && item.End <= previous.To)
            .Select(item => (decimal?)item.QuantityKwh)
            .SumAsync(cancellationToken) ?? 0m;

        return TypedResults.Ok(new SummaryResponse
        {
            From = period.From,
            To = period.To,
            MeteringPointCount = meteringPointCount,
            MeteringPointCountDelta = meteringPointCount - meteringPointCountAtPeriodStart,
            ActiveSupplyPeriods = activeSupplyPeriods,
            ActiveSupplyPeriodsDelta = activeSupplyPeriods - activeSupplyPeriodsAtPeriodStart,
            ConsumptionKwh = consumptionKwh,
            ConsumptionKwhDelta = consumptionKwh - previousConsumptionKwh,
            PreviousConsumptionKwh = previousConsumptionKwh,
        });
    }

    private static async Task<Results<Ok<IReadOnlyList<DailyBucket>>, ProblemHttpResult>> Timeseries(
        string? metric,
        DateTimeOffset? from,
        DateTimeOffset? to,
        EnergyDbContext db,
        CancellationToken cancellationToken)
    {
        if (!TryNormalizePeriod(from, to, out var period, out var problem)) return problem;
        if (!string.Equals(metric, "consumptionKwh", StringComparison.OrdinalIgnoreCase))
        {
            return TypedResults.Problem(
                title: "Invalid metric",
                detail: "Metric must be: consumptionKwh.",
                statusCode: StatusCodes.Status400BadRequest);
        }

        var buckets = await db.ConsumptionIntervals.AsNoTracking()
            .Where(item => item.IsCurrent && item.Start >= period.From && item.End <= period.To)
            .GroupBy(item => item.Start.Date)
            .Select(group => new DailyBucketProjection(group.Key, group.Sum(item => item.QuantityKwh)))
            .OrderBy(item => item.Date)
            .ToListAsync(cancellationToken);
        return TypedResults.Ok<IReadOnlyList<DailyBucket>>(buckets
            .Select(item => new DailyBucket(DateOnly.FromDateTime(item.Date), item.Value))
            .ToArray());
    }

    private static async Task<Ok<IReadOnlyList<AttentionItem>>> Attention(
        EnergyDbContext db,
        CancellationToken cancellationToken)
    {
        var now = DateTimeOffset.UtcNow;
        var expiresBy = now.AddDays(30);
        var items = await db.SupplyPeriods.AsNoTracking()
            .Where(item => item.Status == SupplyPeriodStatus.Active && item.End != null &&
                           item.End >= now && item.End <= expiresBy)
            .OrderBy(item => item.End)
            .Select(item => new AttentionItem
            {
                Id = item.Id.ToString(),
                Type = "supplyPeriodExpiring",
                Title = "Supply period expires soon",
                OccurredAt = item.End!.Value,
                EntityId = item.MeteringPointId.ToString(),
            })
            .ToListAsync(cancellationToken);
        return TypedResults.Ok<IReadOnlyList<AttentionItem>>(items);
    }

    private static bool TryNormalizePeriod(
        DateTimeOffset? from,
        DateTimeOffset? to,
        out Period period,
        out ProblemHttpResult problem)
    {
        var normalizedTo = to ?? DateTimeOffset.UtcNow;
        var normalizedFrom = from ?? normalizedTo.AddDays(-DefaultPeriodDays);
        if (normalizedFrom <= normalizedTo)
        {
            period = new Period(normalizedFrom, normalizedTo);
            problem = null!;
            return true;
        }

        period = default;
        problem = TypedResults.Problem(
            title: "Invalid period",
            detail: "The 'from' value must be earlier than or equal to the 'to' value.",
            statusCode: StatusCodes.Status400BadRequest);
        return false;
    }

    private readonly record struct Period(DateTimeOffset From, DateTimeOffset To)
    {
        internal Period Previous => new(From - (To - From), From);
    }

    private readonly record struct DailyBucketProjection(DateTime Date, decimal Value);

    internal readonly record struct SummaryResponse
    {
        public required DateTimeOffset From { get; init; }
        public required DateTimeOffset To { get; init; }
        public required int MeteringPointCount { get; init; }
        public required int MeteringPointCountDelta { get; init; }
        public required int ActiveSupplyPeriods { get; init; }
        public required int ActiveSupplyPeriodsDelta { get; init; }
        public required decimal ConsumptionKwh { get; init; }
        public required decimal ConsumptionKwhDelta { get; init; }
        public required decimal PreviousConsumptionKwh { get; init; }
    }

    internal readonly record struct DailyBucket(DateOnly Date, decimal Value);

    internal readonly record struct AttentionItem
    {
        public required string Id { get; init; }
        public required string Type { get; init; }
        public required string Title { get; init; }
        public required DateTimeOffset OccurredAt { get; init; }
        public required string EntityId { get; init; }
    }
}