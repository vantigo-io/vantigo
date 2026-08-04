using System.Globalization;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Npgsql;

using Vantigo.Customers.Api.Database.Customers;
using Vantigo.Customers.Api.Domain.Timeline;

namespace Vantigo.Customers.Api.Endpoints.Customers;

internal static class TimelineEndpoints
{
    private const int DefaultLimit = 25;
    private const int MaximumLimit = 100;
    private static readonly string[] ManualTypes =
    [
        "registry.change",
        "interaction.call",
        "interaction.meeting",
        "interaction.email",
        "note",
        "other",
    ];

    internal static async Task<IResult> List(
        int id,
        int? limit,
        string? cursor,
        string? provenance,
        string[]? eventType,
        string? occurredFrom,
        string? occurredTo,
        CustomersDbContext dbContext,
        CancellationToken cancellationToken)
    {
        if (limit is <= 0 or > MaximumLimit)
        {
            return TypedResults.ValidationProblem(
                new Dictionary<string, string[]> { ["limit"] = [$"Limit must be between 1 and {MaximumLimit}"] },
                title: "Invalid timeline query");
        }

        if (!TryParseFilters(provenance, eventType, occurredFrom, occurredTo, out var filters, out var filterErrors))
        {
            return TypedResults.ValidationProblem(filterErrors, title: "Invalid timeline query");
        }

        var customerExists = await dbContext.Customers.AnyAsync(customer => customer.Id == id, cancellationToken);
        if (!customerExists)
        {
            return TypedResults.NotFound();
        }

        TimelineCursor? decodedCursor = null;
        if (cursor is not null && !TryDecodeCursor(cursor, out decodedCursor))
        {
            return TypedResults.ValidationProblem(
                new Dictionary<string, string[]> { ["cursor"] = ["The cursor is malformed"] },
                title: "Invalid timeline query");
        }

        if (decodedCursor is { } cursorWithFilters &&
            (cursorWithFilters.CustomerId != id || cursorWithFilters.FilterDigest != filters.GetDigest(id)))
        {
            return TypedResults.ValidationProblem(
                new Dictionary<string, string[]> { ["cursor"] = ["The cursor does not match the requested timeline filters"] },
                title: "Invalid timeline query");
        }

        var query = dbContext.CustomerTimelineEntries
            .AsNoTracking()
            .Where(entry => entry.CustomerId == id && entry.State == TimelineState.Active);

        if (filters.Provenance is not null)
        {
            query = query.Where(entry => entry.Provenance == filters.Provenance);
        }

        if (filters.EventTypes.Count > 0)
        {
            query = query.Where(entry => filters.EventTypes.Contains(entry.EventType));
        }

        if (filters.OccurredFrom is { } from)
        {
            query = query.Where(entry => entry.OccurredOn >= from);
        }

        if (filters.OccurredTo is { } to)
        {
            query = query.Where(entry => entry.OccurredOn <= to);
        }

        if (decodedCursor is { } after)
        {
            if (after.OccurredAt is { } occurredAt)
            {
                query = query.Where(entry =>
                    entry.OccurredOn < after.OccurredOn ||
                    (entry.OccurredOn == after.OccurredOn &&
                        (entry.OccurredAt == null || entry.OccurredAt < occurredAt ||
                         (entry.OccurredAt == occurredAt && entry.Id < after.Id))));
            }
            else
            {
                query = query.Where(entry =>
                    entry.OccurredOn < after.OccurredOn ||
                    (entry.OccurredOn == after.OccurredOn && entry.OccurredAt == null && entry.Id < after.Id));
            }
        }

        var pageSize = limit ?? DefaultLimit;
        var entries = await query
            .OrderByDescending(entry => entry.OccurredOn)
            .ThenByDescending(entry => entry.OccurredAt.HasValue)
            .ThenByDescending(entry => entry.OccurredAt)
            .ThenByDescending(entry => entry.Id)
            .Take(pageSize + 1)
            .ToListAsync(cancellationToken);

        var hasNext = entries.Count > pageSize;
        if (hasNext)
        {
            entries.RemoveAt(entries.Count - 1);
        }

        var nextCursor = hasNext && entries.Count > 0
            ? EncodeCursor(entries[^1], filters, id)
            : null;

        return TypedResults.Ok(new TimelineListResponse
        {
            Data = entries.Select(TimelineResponse.FromDomain).ToArray(),
            NextCursor = nextCursor,
        });
    }

    internal static async Task<IResult> Create(
        int id,
        ManualTimelineRequest request,
        CustomersDbContext dbContext,
        CancellationToken cancellationToken)
    {
        if (!TryParseManual(request, out var parsed, out var errors))
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid timeline entry");
        }

        var customerExists = await dbContext.Customers.AnyAsync(customer => customer.Id == id, cancellationToken);
        if (!customerExists)
        {
            return TypedResults.NotFound();
        }

        var now = DateTimeOffset.UtcNow;
        var entry = CreateManualEntry(id, parsed, now);
        dbContext.CustomerTimelineEntries.Add(entry);
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.Created($"/api/v1/customers/{id}/timeline/{entry.Id}", TimelineResponse.FromDomain(entry));
    }

    internal static async Task<IResult> Get(
        int id,
        int entryId,
        CustomersDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var entry = await dbContext.CustomerTimelineEntries
            .AsNoTracking()
            .FirstOrDefaultAsync(item => item.Id == entryId && item.CustomerId == id && item.State == TimelineState.Active,
                cancellationToken);

        return entry is null
            ? TypedResults.NotFound()
            : TypedResults.Ok(TimelineResponse.FromDomain(entry));
    }

    internal static async Task<IResult> Update(
        int id,
        int entryId,
        ManualTimelineRequest request,
        CustomersDbContext dbContext,
        CancellationToken cancellationToken)
    {
        if (!TryParseManual(request, out var parsed, out var errors))
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid timeline entry");
        }

        var entry = await dbContext.CustomerTimelineEntries
            .FirstOrDefaultAsync(item => item.Id == entryId && item.CustomerId == id, cancellationToken);
        if (entry is null)
        {
            return TypedResults.NotFound();
        }

        if (entry.Provenance != TimelineProvenance.Manual || entry.State != TimelineState.Active ||
            request.ExpectedRevision != entry.CurrentRevision)
        {
            return Conflict(
                entry.Provenance != TimelineProvenance.Manual || entry.State != TimelineState.Active
                    ? "Timeline entry is immutable"
                    : "Timeline revision conflict",
                entry.Provenance != TimelineProvenance.Manual || entry.State != TimelineState.Active
                    ? "Generated, deleted, or voided timeline entries cannot be edited."
                    : $"The timeline entry has revision {entry.CurrentRevision}; the supplied expectedRevision was {request.ExpectedRevision}.");
        }

        var now = DateTimeOffset.UtcNow;
        ApplyManual(entry, parsed, now);
        AddRevision(entry, now);
        try
        {
            await dbContext.SaveChangesAsync(cancellationToken);
        }
        catch (DbUpdateConcurrencyException)
        {
            return Conflict("Timeline revision conflict", "The timeline entry was changed by another request.");
        }
        catch (DbUpdateException exception) when (IsRevisionConflict(exception))
        {
            return Conflict("Timeline revision conflict", "The timeline entry revision was concurrently changed.");
        }

        return TypedResults.Ok(TimelineResponse.FromDomain(entry));
    }

    internal static async Task<IResult> Delete(
        int id,
        int entryId,
        int? expectedRevision,
        CustomersDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var entry = await dbContext.CustomerTimelineEntries
            .FirstOrDefaultAsync(item => item.Id == entryId && item.CustomerId == id, cancellationToken);
        if (entry is null)
        {
            return TypedResults.NotFound();
        }

        if (entry.Provenance != TimelineProvenance.Manual || entry.State != TimelineState.Active ||
            expectedRevision is null || expectedRevision.Value != entry.CurrentRevision)
        {
            return Conflict(
                entry.Provenance != TimelineProvenance.Manual || entry.State != TimelineState.Active
                    ? "Timeline entry is immutable"
                    : "Timeline revision conflict",
                entry.Provenance != TimelineProvenance.Manual || entry.State != TimelineState.Active
                    ? "Generated, deleted, or voided timeline entries cannot be deleted."
                    : $"The timeline entry has revision {entry.CurrentRevision}; the supplied expectedRevision was {expectedRevision?.ToString() ?? "missing"}.");
        }

        var now = DateTimeOffset.UtcNow;
        entry.State = TimelineState.Deleted;
        entry.DeletedAt = now;
        entry.UpdatedAt = now;
        entry.CurrentRevision++;
        AddRevision(entry, now);
        try
        {
            await dbContext.SaveChangesAsync(cancellationToken);
        }
        catch (DbUpdateConcurrencyException)
        {
            return Conflict("Timeline revision conflict", "The timeline entry was changed by another request.");
        }
        catch (DbUpdateException exception) when (IsRevisionConflict(exception))
        {
            return Conflict("Timeline revision conflict", "The timeline entry revision was concurrently changed.");
        }

        return TypedResults.NoContent();
    }

    internal static async Task<IResult> Revisions(
        int id,
        int entryId,
        CustomersDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var exists = await dbContext.CustomerTimelineEntries
            .AnyAsync(entry => entry.Id == entryId && entry.CustomerId == id, cancellationToken);
        if (!exists)
        {
            return TypedResults.NotFound();
        }

        var revisions = await dbContext.CustomerTimelineEntryRevisions
            .AsNoTracking()
            .Where(revision => revision.CustomerTimelineEntryId == entryId)
            .OrderBy(revision => revision.RevisionNumber)
            .Select(revision => TimelineRevisionResponse.FromDomain(revision))
            .ToListAsync(cancellationToken);

        return TypedResults.Ok(new { data = revisions });
    }

    private static CustomerTimelineEntry CreateManualEntry(int customerId, ParsedManualTimeline parsed, DateTimeOffset now)
    {
        var entry = new CustomerTimelineEntry
        {
            CustomerId = customerId,
            Provenance = TimelineProvenance.Manual,
            Producer = "customers.api",
            EventType = parsed.EventType,
            OccurredOn = parsed.OccurredOn,
            OccurredAt = parsed.OccurredAt,
            Note = parsed.Note,
            Summary = parsed.Note[..Math.Min(parsed.Note.Length, 500)],
            SourceUrl = parsed.SourceUrl,
            PayloadVersion = 1,
            CurrentRevision = 1,
            State = TimelineState.Active,
            ActorKind = TimelineActorKind.Unattributed,
            ActorDisplay = "Unattributed",
            CreatedAt = now,
            UpdatedAt = now,
        };
        AddRevision(entry, now);
        return entry;
    }

    private static void ApplyManual(CustomerTimelineEntry entry, ParsedManualTimeline parsed, DateTimeOffset now)
    {
        entry.EventType = parsed.EventType;
        entry.OccurredOn = parsed.OccurredOn;
        entry.OccurredAt = parsed.OccurredAt;
        entry.Note = parsed.Note;
        entry.Summary = parsed.Note[..Math.Min(parsed.Note.Length, 500)];
        entry.SourceUrl = parsed.SourceUrl;
        entry.UpdatedAt = now;
        entry.CurrentRevision++;
    }

    private static void AddRevision(CustomerTimelineEntry entry, DateTimeOffset now)
    {
        entry.Revisions.Add(new CustomerTimelineEntryRevision
        {
            RevisionNumber = entry.CurrentRevision,
            Provenance = entry.Provenance,
            Producer = entry.Producer,
            EventType = entry.EventType,
            OccurredOn = entry.OccurredOn,
            OccurredAt = entry.OccurredAt,
            Summary = entry.Summary,
            Note = entry.Note,
            SourceUrl = entry.SourceUrl,
            PayloadJson = entry.PayloadJson,
            PayloadVersion = entry.PayloadVersion,
            State = entry.State,
            ActorKind = TimelineActorKind.Unattributed,
            ActorDisplay = "Unattributed",
            CreatedAt = now,
            DeletedAt = entry.DeletedAt,
        });
    }

    private static bool TryParseManual(
        ManualTimelineRequest request,
        out ParsedManualTimeline parsed,
        out Dictionary<string, string[]> errors)
    {
        errors = new Dictionary<string, string[]>();
        parsed = default;

        if (string.IsNullOrWhiteSpace(request.EventType) || !ManualTypes.Contains(request.EventType, StringComparer.Ordinal))
        {
            errors["eventType"] = [$"EventType must be one of: {string.Join(", ", ManualTypes)}"];
        }

        if (!DateOnly.TryParseExact(request.OccurredOn, "yyyy-MM-dd", CultureInfo.InvariantCulture,
                DateTimeStyles.None, out var occurredOn))
        {
            errors["occurredOn"] = ["OccurredOn must be an ISO date (yyyy-MM-dd)"];
        }
        else if (occurredOn > DateOnly.FromDateTime(DateTime.UtcNow))
        {
            errors["occurredOn"] = ["An occurrence date cannot be in the future"];
        }

        var note = request.Note;
        if (string.IsNullOrWhiteSpace(note))
        {
            errors["note"] = ["A nonblank note or description is required"];
        }
        else if (note.Length > 10000)
        {
            errors["note"] = ["A note cannot be longer than 10000 characters"];
        }

        DateTimeOffset? occurredAt = request.OccurredAt?.ToUniversalTime();
        if (occurredAt > DateTimeOffset.UtcNow)
        {
            errors["occurredAt"] = ["An occurrence instant cannot be in the future"];
        }
        else if (occurredAt is { } instant &&
                 DateOnly.FromDateTime(instant.UtcDateTime) != occurredOn)
        {
            errors["occurredAt"] = ["OccurredAt must have the same UTC calendar date as occurredOn"];
        }

        if (request.SourceUrl is { } sourceUrl)
        {
            if (!Uri.TryCreate(sourceUrl, UriKind.Absolute, out var uri) ||
                (uri.Scheme != Uri.UriSchemeHttp && uri.Scheme != Uri.UriSchemeHttps))
            {
                errors["sourceUrl"] = ["SourceUrl must be an absolute http(s) URL"];
            }
            else if (sourceUrl.Length > 2048)
            {
                errors["sourceUrl"] = ["SourceUrl cannot be longer than 2048 characters"];
            }
        }

        if (errors.Count == 0)
        {
            parsed = new ParsedManualTimeline(request.EventType!, occurredOn, occurredAt, note!.Trim(), request.SourceUrl);
            return true;
        }

        return false;
    }

    private static bool IsRevisionConflict(DbUpdateException exception) =>
        exception.GetBaseException() is PostgresException postgresException &&
        postgresException.SqlState == PostgresErrorCodes.UniqueViolation &&
        postgresException.ConstraintName == "ux_customers_timeline_entries_revisions_entry_revision";

    private static IResult Conflict(string title, string detail) =>
        TypedResults.Problem(title: title, detail: detail, statusCode: StatusCodes.Status409Conflict);

    private static string EncodeCursor(CustomerTimelineEntry entry, TimelineFilters filters, int customerId)
    {
        var json = JsonSerializer.Serialize(new TimelineCursor(
            customerId, entry.OccurredOn, entry.OccurredAt, entry.Id, filters.GetDigest(customerId)));
        return Convert.ToBase64String(Encoding.UTF8.GetBytes(json))
            .Replace('+', '-').Replace('/', '_').TrimEnd('=');
    }

    private static bool TryDecodeCursor(string value, out TimelineCursor? cursor)
    {
        cursor = null;
        try
        {
            var base64 = value.Replace('-', '+').Replace('_', '/') + new string('=', (4 - value.Length % 4) % 4);
            var decoded = JsonSerializer.Deserialize<TimelineCursor>(Convert.FromBase64String(base64));
            if (decoded is not { Id: > 0 } || decoded.OccurredOn == DateOnly.MinValue || decoded.FilterDigest is null)
            {
                return false;
            }

            cursor = decoded;
            return true;
        }
        catch (FormatException)
        {
            return false;
        }
        catch (JsonException)
        {
            return false;
        }
    }

    private static bool TryParseFilters(
        string? provenance,
        string[]? eventTypes,
        string? occurredFrom,
        string? occurredTo,
        out TimelineFilters filters,
        out Dictionary<string, string[]> errors)
    {
        errors = [];
        filters = default;

        if (provenance is not null && provenance is not (TimelineProvenance.Manual or TimelineProvenance.Generated))
        {
            errors["provenance"] = ["Provenance must be either 'manual' or 'generated'"];
        }

        var normalizedTypes = eventTypes ?? [];
        for (var index = 0; index < normalizedTypes.Length; index++)
        {
            if (string.IsNullOrWhiteSpace(normalizedTypes[index]))
            {
                errors[$"eventType[{index}]"] = ["EventType cannot be blank"];
            }
            else if (normalizedTypes[index].Length > 100)
            {
                errors[$"eventType[{index}]"] = ["EventType cannot be longer than 100 characters"];
            }
        }

        DateOnly? from = ParseFilterDate(occurredFrom, "occurredFrom", errors);
        DateOnly? to = ParseFilterDate(occurredTo, "occurredTo", errors);
        if (from is { } start && to is { } end && start > end)
        {
            errors["occurredFrom"] = ["OccurredFrom cannot be later than occurredTo"];
        }

        if (errors.Count == 0)
        {
            filters = new TimelineFilters(provenance, normalizedTypes, from, to);
            return true;
        }

        return false;
    }

    private static DateOnly? ParseFilterDate(string? value, string key, Dictionary<string, string[]> errors)
    {
        if (value is null)
        {
            return null;
        }

        if (DateOnly.TryParseExact(value, "yyyy-MM-dd", CultureInfo.InvariantCulture,
                DateTimeStyles.None, out var parsed))
        {
            return parsed;
        }

        errors[key] = [$"{key} must be an ISO date (yyyy-MM-dd)"];
        return null;
    }

    internal sealed record ManualTimelineRequest
    {
        [JsonPropertyName("eventType")]
        public string? EventType { get; init; }
        public string? OccurredOn { get; init; }
        public DateTimeOffset? OccurredAt { get; init; }
        public string? Note { get; init; }
        public string? SourceUrl { get; init; }
        public int ExpectedRevision { get; init; }
    }

    private readonly record struct ParsedManualTimeline(
        string EventType,
        DateOnly OccurredOn,
        DateTimeOffset? OccurredAt,
        string Note,
        string? SourceUrl);

    private readonly record struct TimelineCursor(
        int CustomerId,
        DateOnly OccurredOn,
        DateTimeOffset? OccurredAt,
        int Id,
        string? FilterDigest);

    private readonly record struct TimelineFilters(
        string? Provenance,
        IReadOnlyList<string> EventTypes,
        DateOnly? OccurredFrom,
        DateOnly? OccurredTo)
    {
        public string GetDigest(int customerId)
        {
            var canonical = JsonSerializer.Serialize(new
            {
                customerId,
                provenance = Provenance,
                eventTypes = EventTypes.Order(StringComparer.Ordinal).Distinct(StringComparer.Ordinal).ToArray(),
                occurredFrom = OccurredFrom?.ToString("yyyy-MM-dd", CultureInfo.InvariantCulture),
                occurredTo = OccurredTo?.ToString("yyyy-MM-dd", CultureInfo.InvariantCulture),
            });
            return Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(canonical))).ToLowerInvariant();
        }
    }
}

internal sealed class TimelineListResponse
{
    public required IReadOnlyList<TimelineResponse> Data { get; init; }
    public string? NextCursor { get; init; }
}

internal sealed class TimelineRevisionListResponse
{
    public required IReadOnlyList<TimelineRevisionResponse> Data { get; init; }
}

internal sealed class TimelineResponse
{
    public required int Id { get; init; }
    [JsonPropertyName("eventType")]
    public required string EventType { get; init; }
    public required string Provenance { get; init; }
    public required string Producer { get; init; }
    public required DateOnly OccurredOn { get; init; }
    public DateTimeOffset? OccurredAt { get; init; }
    public string? Summary { get; init; }
    public string? Note { get; init; }
    public string? SourceUrl { get; init; }
    public JsonElement? Payload { get; init; }
    public required int CurrentRevision { get; init; }
    public required string State { get; init; }
    public required string ActorKind { get; init; }
    public string? ActorDisplay { get; init; }
    public required DateTimeOffset CreatedAt { get; init; }
    public required DateTimeOffset UpdatedAt { get; init; }

    internal static TimelineResponse FromDomain(CustomerTimelineEntry entry) => new()
    {
        Id = entry.Id,
        EventType = entry.EventType,
        Provenance = entry.Provenance,
        Producer = entry.Producer,
        OccurredOn = entry.OccurredOn,
        OccurredAt = entry.OccurredAt,
        Summary = entry.Summary,
        Note = entry.Note,
        SourceUrl = entry.SourceUrl,
        Payload = ParsePayload(entry.PayloadJson),
        CurrentRevision = entry.CurrentRevision,
        State = entry.State,
        ActorKind = entry.ActorKind,
        ActorDisplay = entry.ActorDisplay,
        CreatedAt = entry.CreatedAt,
        UpdatedAt = entry.UpdatedAt,
    };

    private static JsonElement? ParsePayload(string? payload) => payload is null ? null : JsonSerializer.Deserialize<JsonElement>(payload);
}

internal sealed class TimelineRevisionResponse
{
    public required int Revision { get; init; }
    [JsonPropertyName("eventType")]
    public required string EventType { get; init; }
    public required string Action { get; init; }
    public required DateTimeOffset ChangedAt { get; init; }
    public required string Provenance { get; init; }
    public required string Producer { get; init; }
    public required DateOnly OccurredOn { get; init; }
    public DateTimeOffset? OccurredAt { get; init; }
    public string? Summary { get; init; }
    public string? Note { get; init; }
    public string? SourceUrl { get; init; }
    public JsonElement? Payload { get; init; }
    public required string State { get; init; }
    public required string ActorKind { get; init; }
    public required string ActorDisplayName { get; init; }
    public DateTimeOffset? DeletedAt { get; init; }

    internal static TimelineRevisionResponse FromDomain(CustomerTimelineEntryRevision revision) => new()
    {
        Revision = revision.RevisionNumber,
        EventType = revision.EventType,
        Action = revision.State == TimelineState.Deleted
            ? "delete"
            : revision.RevisionNumber == 1 ? "create" : "update",
        ChangedAt = revision.CreatedAt,
        Provenance = revision.Provenance,
        Producer = revision.Producer,
        OccurredOn = revision.OccurredOn,
        OccurredAt = revision.OccurredAt,
        Summary = revision.Summary,
        Note = revision.Note,
        SourceUrl = revision.SourceUrl,
        Payload = revision.PayloadJson is null ? null : JsonSerializer.Deserialize<JsonElement>(revision.PayloadJson),
        State = revision.State,
        ActorKind = revision.ActorKind,
        ActorDisplayName = string.IsNullOrWhiteSpace(revision.ActorDisplay)
            ? "Unattributed"
            : revision.ActorDisplay,
        DeletedAt = revision.DeletedAt,
    };
}