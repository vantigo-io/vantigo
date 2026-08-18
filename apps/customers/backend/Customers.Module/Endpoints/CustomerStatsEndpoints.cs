using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Contracts.AspNetCore.Authorization;
using Vantigo.Customers.Authorization;
using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Domain.Customers.Common;

namespace Vantigo.Customers.Endpoints;

internal static class CustomerStatsEndpoints
{
    private const int DefaultPeriodDays = 30;

    internal static void MapCustomerStatsEndpoints(this IEndpointRouteBuilder api)
    {
        api.MapGet("/stats/summary", Summary)
            .WithSummary("Get customer dashboard summary")
            .Produces<SummaryResponse>()
            .RequirePermission(CustomerPermissions.View);
        api.MapGet("/stats/timeseries", Timeseries)
            .WithSummary("Get customer dashboard time series")
            .Produces<IReadOnlyList<DailyBucket>>()
            .ProducesProblem(StatusCodes.Status400BadRequest)
            .RequirePermission(CustomerPermissions.View);
        api.MapGet("/stats/attention", Attention)
            .WithSummary("Get customer dashboard attention items")
            .Produces<IReadOnlyList<AttentionItem>>()
            .RequirePermission(CustomerPermissions.View);
    }

    private static async Task<Results<Ok<SummaryResponse>, ProblemHttpResult>> Summary(
        DateTimeOffset? from,
        DateTimeOffset? to,
        CustomersDbContext db,
        CancellationToken cancellationToken)
    {
        if (!TryNormalizePeriod(from, to, out var period, out var problem)) return problem;

        var previous = period.Previous;
        var customers = db.Customers.AsNoTracking();
        var contacts = db.Contacts.AsNoTracking();

        var active = await customers.CountAsync(item => item.Status == (CustomerStatus)CustomerStatus.Active, cancellationToken);
        var activeAtPeriodStart = await customers.CountAsync(
            item => item.Status == (CustomerStatus)CustomerStatus.Active && item.CreatedAt < period.From,
            cancellationToken);
        var newCustomers = await customers.CountAsync(
            item => item.CreatedAt >= period.From && item.CreatedAt < period.To, cancellationToken);
        var previousNewCustomers = await customers.CountAsync(
            item => item.CreatedAt >= previous.From && item.CreatedAt < previous.To, cancellationToken);
        var newContacts = await contacts.CountAsync(
            item => item.CreatedAt >= period.From && item.CreatedAt < period.To, cancellationToken);
        var previousNewContacts = await contacts.CountAsync(
            item => item.CreatedAt >= previous.From && item.CreatedAt < previous.To, cancellationToken);

        return TypedResults.Ok(new SummaryResponse
        {
            From = period.From,
            To = period.To,
            TotalActiveCustomers = active,
            TotalActiveCustomersDelta = active - activeAtPeriodStart,
            NewCustomers = newCustomers,
            NewCustomersDelta = newCustomers - previousNewCustomers,
            NewContacts = newContacts,
            NewContactsDelta = newContacts - previousNewContacts,
        });
    }

    private static async Task<Results<Ok<IReadOnlyList<DailyBucket>>, ProblemHttpResult>> Timeseries(
        string? metric,
        DateTimeOffset? from,
        DateTimeOffset? to,
        CustomersDbContext db,
        CancellationToken cancellationToken)
    {
        if (!TryNormalizePeriod(from, to, out var period, out var problem)) return problem;
        if (!string.Equals(metric, "newCustomers", StringComparison.OrdinalIgnoreCase) &&
            !string.Equals(metric, "newContacts", StringComparison.OrdinalIgnoreCase))
        {
            return TypedResults.Problem(
                title: "Invalid metric",
                detail: "Metric must be one of: newCustomers, newContacts.",
                statusCode: StatusCodes.Status400BadRequest);
        }

        if (string.Equals(metric, "newCustomers", StringComparison.OrdinalIgnoreCase))
        {
            var buckets = await db.Customers.AsNoTracking()
                .Where(item => item.CreatedAt >= period.From && item.CreatedAt < period.To)
                .GroupBy(item => item.CreatedAt.Date)
                .Select(group => new DailyBucketProjection(group.Key, group.LongCount()))
                .OrderBy(item => item.Date)
                .ToListAsync(cancellationToken);
            return TypedResults.Ok<IReadOnlyList<DailyBucket>>(buckets
                .Select(item => new DailyBucket(DateOnly.FromDateTime(item.Date), item.Value))
                .ToArray());
        }

        var contactBuckets = await db.Contacts.AsNoTracking()
            .Where(item => item.CreatedAt >= period.From && item.CreatedAt < period.To)
            .GroupBy(item => item.CreatedAt.Date)
            .Select(group => new DailyBucketProjection(group.Key, group.LongCount()))
            .OrderBy(item => item.Date)
            .ToListAsync(cancellationToken);
        return TypedResults.Ok<IReadOnlyList<DailyBucket>>(contactBuckets
            .Select(item => new DailyBucket(DateOnly.FromDateTime(item.Date), item.Value))
            .ToArray());
    }

    private static Task<Ok<IReadOnlyList<AttentionItem>>> Attention() =>
        Task.FromResult(TypedResults.Ok<IReadOnlyList<AttentionItem>>([]));

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

    private readonly record struct DailyBucketProjection(DateTime Date, long Value);

    internal readonly record struct SummaryResponse
    {
        public required DateTimeOffset From { get; init; }
        public required DateTimeOffset To { get; init; }
        public required int TotalActiveCustomers { get; init; }
        public required int TotalActiveCustomersDelta { get; init; }
        public required int NewCustomers { get; init; }
        public required int NewCustomersDelta { get; init; }
        public required int NewContacts { get; init; }
        public required int NewContactsDelta { get; init; }
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
}