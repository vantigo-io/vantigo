using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Communications.Authorization;
using Vantigo.Communications.Database.Communications;
using Vantigo.Contracts.AspNetCore.Authorization;

namespace Vantigo.Communications.Endpoints;

internal static class CommunicationsStatsEndpoints
{
    private const int DefaultPeriodDays = 30;

    internal static void MapCommunicationsStatsEndpoints(this IEndpointRouteBuilder api)
    {
        api.MapGet("/stats/summary", Summary)
            .WithSummary("Get communications dashboard summary")
            .Produces<SummaryResponse>()
            .ProducesProblem(StatusCodes.Status400BadRequest)
            .RequirePermission(CommunicationsPermissions.ConversationsView);
        api.MapGet("/stats/timeseries", Timeseries)
            .WithSummary("Get communications dashboard time series")
            .Produces<IReadOnlyList<DailyBucket>>()
            .ProducesProblem(StatusCodes.Status400BadRequest)
            .RequirePermission(CommunicationsPermissions.ConversationsView);
        api.MapGet("/stats/attention", Attention)
            .WithSummary("Get communications dashboard attention items")
            .Produces<IReadOnlyList<AttentionItem>>()
            .RequirePermission(CommunicationsPermissions.ConversationsView);
    }

    private static async Task<Results<Ok<SummaryResponse>, ProblemHttpResult>> Summary(
        DateTimeOffset? from,
        DateTimeOffset? to,
        CommunicationsDbContext db,
        CancellationToken cancellationToken)
    {
        if (!TryNormalizePeriod(from, to, out var period, out var problem)) return problem;

        var previous = period.Previous;
        var open = await db.Conversations.CountAsync(item => item.Status == "open", cancellationToken);
        var openBeforePeriod = await db.Conversations.CountAsync(
            item => item.Status == "open" && item.CreatedAt < period.From, cancellationToken);
        var newConversations = await db.Conversations.CountAsync(
            item => item.CreatedAt >= period.From && item.CreatedAt < period.To, cancellationToken);
        var previousNewConversations = await db.Conversations.CountAsync(
            item => item.CreatedAt >= previous.From && item.CreatedAt < previous.To, cancellationToken);
        var messages = await db.ConversationMessages.CountAsync(
            item => item.OccurredAt >= period.From && item.OccurredAt < period.To, cancellationToken);
        var previousMessages = await db.ConversationMessages.CountAsync(
            item => item.OccurredAt >= previous.From && item.OccurredAt < previous.To, cancellationToken);
        var closed = await db.Conversations.CountAsync(
            item => item.Status == "closed" && item.LastActivityAt >= period.From && item.LastActivityAt < period.To,
            cancellationToken);
        var previousClosed = await db.Conversations.CountAsync(
            item => item.Status == "closed" && item.LastActivityAt >= previous.From && item.LastActivityAt < previous.To,
            cancellationToken);

        return TypedResults.Ok(new SummaryResponse
        {
            From = period.From,
            To = period.To,
            OpenConversations = open,
            OpenConversationsDelta = open - openBeforePeriod,
            NewConversations = newConversations,
            NewConversationsDelta = newConversations - previousNewConversations,
            Messages = messages,
            MessagesDelta = messages - previousMessages,
            ClosedConversations = closed,
            ClosedConversationsDelta = closed - previousClosed,
        });
    }

    private static async Task<Results<Ok<IReadOnlyList<DailyBucket>>, ProblemHttpResult>> Timeseries(
        string? metric,
        DateTimeOffset? from,
        DateTimeOffset? to,
        CommunicationsDbContext db,
        CancellationToken cancellationToken)
    {
        if (!TryNormalizePeriod(from, to, out var period, out var problem)) return problem;
        var normalizedMetric = metric?.Trim().ToLowerInvariant();
        if (normalizedMetric is not ("newconversations" or "messages"))
        {
            return TypedResults.Problem(
                title: "Invalid metric",
                detail: "Metric must be one of: newConversations, messages.",
                statusCode: StatusCodes.Status400BadRequest);
        }

        if (normalizedMetric == "newconversations")
        {
            var conversationBuckets = await db.Conversations.AsNoTracking()
                .Where(item => item.CreatedAt >= period.From && item.CreatedAt < period.To)
                .GroupBy(item => item.CreatedAt.Date)
                .Select(group => new DailyBucketProjection(group.Key, group.LongCount()))
                .OrderBy(item => item.Date)
                .ToListAsync(cancellationToken);
            return TypedResults.Ok<IReadOnlyList<DailyBucket>>(conversationBuckets
                .Select(item => new DailyBucket(DateOnly.FromDateTime(item.Date), item.Value))
                .ToArray());
        }

        var messageBuckets = await db.ConversationMessages.AsNoTracking()
            .Where(item => item.OccurredAt >= period.From && item.OccurredAt < period.To)
            .GroupBy(item => item.OccurredAt.Date)
            .Select(group => new DailyBucketProjection(group.Key, group.LongCount()))
            .OrderBy(item => item.Date)
            .ToListAsync(cancellationToken);
        return TypedResults.Ok<IReadOnlyList<DailyBucket>>(messageBuckets
            .Select(item => new DailyBucket(DateOnly.FromDateTime(item.Date), item.Value))
            .ToArray());
    }

    private static async Task<Ok<IReadOnlyList<AttentionItem>>> Attention(
        CommunicationsDbContext db,
        CancellationToken cancellationToken)
    {
        var cutoff = DateTimeOffset.UtcNow.AddHours(-24);
        var failedDeliveries = db.MessageDeliveries.AsNoTracking()
            .Where(item => item.Status == "failed" || item.Status == "submission_failed")
            .Select(item => new AttentionItem
            {
                Id = item.Id.ToString(),
                Type = "failedDelivery",
                Title = "Message delivery failed",
                OccurredAt = item.CreatedAt,
                EntityId = item.MessageId.ToString(),
            });

        var unanswered = db.Conversations.AsNoTracking()
            .Where(item => item.Status == "open" && item.LastActivityAt < cutoff)
            .Where(item => item.Messages
                .OrderByDescending(message => message.OccurredAt)
                .Select(message => message.Direction)
                .FirstOrDefault() == "inbound")
            .Select(item => new AttentionItem
            {
                Id = item.Id.ToString(),
                Type = "conversationNoReply",
                Title = item.Subject ?? "Conversation awaiting reply",
                OccurredAt = item.LastActivityAt,
                EntityId = item.Id.ToString(),
            });

        var items = await failedDeliveries.Concat(unanswered)
            .OrderBy(item => item.OccurredAt)
            .Take(100)
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

    private readonly record struct DailyBucketProjection(DateTime Date, long Value);

    internal readonly record struct SummaryResponse
    {
        public required DateTimeOffset From { get; init; }
        public required DateTimeOffset To { get; init; }
        public required int OpenConversations { get; init; }
        public required int OpenConversationsDelta { get; init; }
        public required int NewConversations { get; init; }
        public required int NewConversationsDelta { get; init; }
        public required int Messages { get; init; }
        public required int MessagesDelta { get; init; }
        public required int ClosedConversations { get; init; }
        public required int ClosedConversationsDelta { get; init; }
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