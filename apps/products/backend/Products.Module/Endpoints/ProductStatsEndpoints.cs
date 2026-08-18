using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Contracts.AspNetCore.Authorization;
using Vantigo.Products.Authorization;
using Vantigo.Products.Database.Products;
using Vantigo.Products.Domain.Products;

namespace Vantigo.Products.Endpoints;

internal static class ProductStatsEndpoints
{
    private const int DefaultPeriodDays = 30;

    internal static void MapProductStatsEndpoints(this IEndpointRouteBuilder api)
    {
        api.MapGet("/stats/summary", Summary)
            .WithSummary("Get products dashboard summary")
            .Produces<SummaryResponse>()
            .ProducesProblem(StatusCodes.Status400BadRequest)
            .RequireProductReadPermissions();
        api.MapGet("/stats/timeseries", Timeseries)
            .WithSummary("Get products dashboard time series")
            .Produces<IReadOnlyList<DailyBucket>>()
            .ProducesProblem(StatusCodes.Status400BadRequest)
            .RequireProductReadPermissions();
        api.MapGet("/stats/attention", Attention)
            .WithSummary("Get products dashboard attention items")
            .Produces<IReadOnlyList<AttentionItem>>()
            .RequireProductReadPermissions();
    }

    private static async Task<Results<Ok<SummaryResponse>, ProblemHttpResult>> Summary(
        DateTimeOffset? from,
        DateTimeOffset? to,
        ProductsDbContext db,
        CancellationToken cancellationToken)
    {
        if (!TryNormalizePeriod(from, to, out var period, out var problem)) return problem;

        var previous = period.Previous;
        var products = db.Products.AsNoTracking();
        var active = await products.CountAsync(item => item.Status == ProductStatus.Active, cancellationToken);
        var activeAtPeriodStart = await products.CountAsync(
            item => item.Status == ProductStatus.Active && item.CreatedAt < period.From, cancellationToken);
        var newProducts = await products.CountAsync(
            item => item.CreatedAt >= period.From && item.CreatedAt < period.To, cancellationToken);
        var previousNewProducts = await products.CountAsync(
            item => item.CreatedAt >= previous.From && item.CreatedAt < previous.To, cancellationToken);

        var statusCounts = await products
            .GroupBy(item => item.Status)
            .Select(group => new StatusCountProjection(group.Key, group.LongCount()))
            .ToListAsync(cancellationToken);
        var previousStatusCounts = await products
            .Where(item => item.CreatedAt < period.From)
            .GroupBy(item => item.Status)
            .Select(group => new StatusCountProjection(group.Key, group.LongCount()))
            .ToListAsync(cancellationToken);

        var counts = CreateStatusDictionary(statusCounts);
        var deltas = CreateStatusDictionary(statusCounts, previousStatusCounts);
        return TypedResults.Ok(new SummaryResponse
        {
            From = period.From,
            To = period.To,
            TotalActiveProducts = active,
            TotalActiveProductsDelta = active - activeAtPeriodStart,
            NewProducts = newProducts,
            NewProductsDelta = newProducts - previousNewProducts,
            StatusCounts = counts,
            StatusCountDeltas = deltas,
        });
    }

    private static async Task<Results<Ok<IReadOnlyList<DailyBucket>>, ProblemHttpResult>> Timeseries(
        string? metric,
        DateTimeOffset? from,
        DateTimeOffset? to,
        ProductsDbContext db,
        CancellationToken cancellationToken)
    {
        if (!TryNormalizePeriod(from, to, out var period, out var problem)) return problem;
        if (!string.Equals(metric, "newProducts", StringComparison.OrdinalIgnoreCase))
        {
            return TypedResults.Problem(
                title: "Invalid metric",
                detail: "Metric must be: newProducts.",
                statusCode: StatusCodes.Status400BadRequest);
        }

        var buckets = await db.Products.AsNoTracking()
            .Where(item => item.CreatedAt >= period.From && item.CreatedAt < period.To)
            .GroupBy(item => item.CreatedAt.Date)
            .Select(group => new DailyBucketProjection(group.Key, group.LongCount()))
            .OrderBy(item => item.Date)
            .ToListAsync(cancellationToken);
        return TypedResults.Ok<IReadOnlyList<DailyBucket>>(buckets
            .Select(item => new DailyBucket(DateOnly.FromDateTime(item.Date), item.Value))
            .ToArray());
    }

    private static Task<Ok<IReadOnlyList<AttentionItem>>> Attention() =>
        Task.FromResult(TypedResults.Ok<IReadOnlyList<AttentionItem>>([]));

    private static Dictionary<string, long> CreateStatusDictionary(
        IReadOnlyCollection<StatusCountProjection> current,
        IReadOnlyCollection<StatusCountProjection>? previous = null)
    {
        var previousByStatus = previous?.ToDictionary(item => item.Status, item => item.Value) ?? [];
        return Enum.GetValues<ProductStatus>()
            .ToDictionary(
                status => status.ToString().ToLowerInvariant(),
                status => current.Where(item => item.Status == status).Select(item => item.Value).SingleOrDefault() -
                           (previous is null ? 0 : previousByStatus.GetValueOrDefault(status)));
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

    private readonly record struct StatusCountProjection(ProductStatus Status, long Value);
    private readonly record struct DailyBucketProjection(DateTime Date, long Value);

    internal readonly record struct SummaryResponse
    {
        public required DateTimeOffset From { get; init; }
        public required DateTimeOffset To { get; init; }
        public required int TotalActiveProducts { get; init; }
        public required int TotalActiveProductsDelta { get; init; }
        public required int NewProducts { get; init; }
        public required int NewProductsDelta { get; init; }
        public required IReadOnlyDictionary<string, long> StatusCounts { get; init; }
        public required IReadOnlyDictionary<string, long> StatusCountDeltas { get; init; }
    }

    internal readonly record struct DailyBucket(DateOnly Date, long Value);

    internal readonly record struct AttentionItem
    {
        public required string Id { get; init; }
        public required string Type { get; init; }
        public required string Title { get; init; }
        public required DateTimeOffset OccurredAt { get; init; }
        public required string EntityId { get; init; }
    }

    private static TBuilder RequireProductReadPermissions<TBuilder>(this TBuilder endpoint)
        where TBuilder : IEndpointConventionBuilder => endpoint
            .RequirePermission("products:products-view")
            .RequirePermission("products:variants-view")
            .RequirePermission("products:pricing-view")
            .RequirePermission("products:categories-view")
            .RequirePermission("products:tax-categories-view");
}