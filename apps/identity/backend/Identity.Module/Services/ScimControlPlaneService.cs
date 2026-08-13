using System.Security.Cryptography;

using Microsoft.EntityFrameworkCore;

using Npgsql;

using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Endpoints.Auth;

namespace Vantigo.Identity.Services;

public sealed class ScimControlPlaneService(
    AccountsDbContext dbContext,
    ScimTokenService tokenService,
    ScimLifecycleService lifecycleService,
    AuthorizationAuditWriter auditWriter,
    IHttpContextAccessor httpContextAccessor,
    TimeProvider? timeProvider = null)
{
    internal static Func<CancellationToken, Task>? OverrideBeforeSaveAsync { get; set; }

    private TimeProvider Clock => timeProvider ?? TimeProvider.System;

    public async Task<IResult> ListAsync(CancellationToken token) => TypedResults.Ok(await dbContext.ScimConnections.AsNoTracking().Select(item => new
    {
        item.Id,
        item.FederationConnectionId,
        Mode = item.Mode.ToString(),
        item.IsEnabled,
        item.TokenVersion,
        item.CreatedAt,
        item.UpdatedAt,
        item.LastRotatedAt,
        item.LastRevokedAt,
        item.ConcurrencyStamp,
    }).ToArrayAsync(token));

    public async Task<IResult> CreateAsync(ScimConnectionRequest request, CancellationToken token)
    {
        if (!request.TryGetProvisioningMode(out var mode)) return TypedResults.BadRequest(new { code = "invalid_mode" });
        await using var transaction = await dbContext.Database.BeginTransactionAsync(token);
        try
        {
            if (!await dbContext.FederationConnections.AnyAsync(item => item.Id == request.FederationConnectionId, token)) return TypedResults.NotFound();
            if (await dbContext.ScimConnections.AnyAsync(item => item.FederationConnectionId == request.FederationConnectionId, token)) return Conflict();

            var now = Clock.GetUtcNow();
            var connection = new ScimConnection { FederationConnectionId = request.FederationConnectionId, Mode = mode, ConcurrencyStamp = Guid.NewGuid().ToString("N"), CreatedAt = now, UpdatedAt = now };
            dbContext.ScimConnections.Add(connection);
            await dbContext.SaveChangesAsync(token);
            var created = await tokenService.CreateAsync(connection.Id, connection.TokenVersion, now, token);
            await AuditAsync("scim.connection.created", null, new { connection.Id, connection.FederationConnectionId, Mode = connection.Mode.ToString(), connection.TokenVersion }, token);
            await transaction.CommitAsync(token);
            return TypedResults.Created($"/api/v1/identity/access/scim/{connection.Id}", new { connection.Id, Mode = connection.Mode.ToString(), token = created.Plaintext, tokenVersion = connection.TokenVersion, connection.ConcurrencyStamp });
        }
        catch (DbUpdateConcurrencyException)
        {
            return Conflict();
        }
        catch (Exception exception) when (IsConflict(exception))
        {
            return Conflict();
        }
    }

    public async Task<IResult> RotateAsync(Guid id, ScimRotateRequest request, CancellationToken token)
    {
        await using var transaction = await dbContext.Database.BeginTransactionAsync(token);
        try
        {
            var connection = await dbContext.ScimConnections.SingleOrDefaultAsync(item => item.Id == id, token);
            if (connection is null) return TypedResults.NotFound();
            if (!Matches(connection.ConcurrencyStamp, request.ConcurrencyStamp)) return Conflict();

            var now = Clock.GetUtcNow();
            var active = await dbContext.ScimBearerTokens
                .Where(item => item.ScimConnectionId == id && item.RevokedAt == null)
                .OrderByDescending(item => item.CreatedAt)
                .ThenByDescending(item => item.Id)
                .ToListAsync(token);
            var previous = active.FirstOrDefault(item => item.IsCurrent);
            foreach (var item in active)
            {
                item.IsCurrent = false;
                if (item != previous) item.RevokedAt = now;
            }
            if (previous is not null) previous.ExpiresAt = now.Add(ScimTokenService.RotationOverlap);

            var before = new { connection.TokenVersion, connection.IsEnabled, connection.LastRotatedAt };
            connection.TokenVersion++;
            connection.LastRotatedAt = now;
            connection.UpdatedAt = now;
            connection.ConcurrencyStamp = NewStamp();
            var created = await tokenService.CreateAsync(id, connection.TokenVersion, now, token);
            await dbContext.SaveChangesAsync(token);
            await AuditAsync("scim.token.rotated", before, new { connection.TokenVersion, connection.IsEnabled, connection.LastRotatedAt }, token);
            await transaction.CommitAsync(token);
            return TypedResults.Ok(new { connection.Id, token = created.Plaintext, tokenVersion = connection.TokenVersion, connection.ConcurrencyStamp });
        }
        catch (Exception exception) when (IsConflict(exception))
        {
            return Conflict();
        }
    }

    public async Task<IResult> SetEnabledAsync(Guid id, bool enabled, string concurrencyStamp, CancellationToken token)
    {
        await using var transaction = await dbContext.Database.BeginTransactionAsync(token);
        try
        {
            var item = await dbContext.ScimConnections.SingleOrDefaultAsync(connection => connection.Id == id, token);
            if (item is null) return TypedResults.NotFound();
            if (!Matches(item.ConcurrencyStamp, concurrencyStamp)) return Conflict();

            var before = new { item.IsEnabled, item.UpdatedAt };
            var now = Clock.GetUtcNow();
            item.IsEnabled = enabled;
            item.UpdatedAt = now;
            item.ConcurrencyStamp = NewStamp();
            await dbContext.SaveChangesAsync(token);
            await AuditAsync(enabled ? "scim.connection.enabled" : "scim.connection.disabled", before, new { item.IsEnabled, item.UpdatedAt }, token);
            await transaction.CommitAsync(token);
            return TypedResults.Ok(new { item.Id, item.IsEnabled, item.ConcurrencyStamp });
        }
        catch (Exception exception) when (IsConflict(exception))
        {
            return Conflict();
        }
    }

    public async Task<IResult> RevokeAsync(Guid id, ScimRevokeRequest request, CancellationToken token)
    {
        await using var transaction = await dbContext.Database.BeginTransactionAsync(token);
        try
        {
            var item = await dbContext.ScimConnections.SingleOrDefaultAsync(connection => connection.Id == id, token);
            if (item is null) return TypedResults.NotFound();
            if (!Matches(item.ConcurrencyStamp, request.ConcurrencyStamp)) return Conflict();

            var now = Clock.GetUtcNow();
            var tokens = await dbContext.ScimBearerTokens
                .Where(tokenItem => tokenItem.ScimConnectionId == id && tokenItem.RevokedAt == null)
                .ToListAsync(token);
            foreach (var tokenItem in tokens)
            {
                tokenItem.RevokedAt = now;
                tokenItem.IsCurrent = false;
            }
            var before = new { item.IsEnabled, item.LastRevokedAt, item.TokenVersion };
            item.LastRevokedAt = now;
            item.UpdatedAt = now;
            item.IsEnabled = false;
            item.ConcurrencyStamp = NewStamp();
            await dbContext.SaveChangesAsync(token);
            await AuditAsync("scim.token.revoked", before, new { item.IsEnabled, item.LastRevokedAt, item.TokenVersion }, token);
            await transaction.CommitAsync(token);
            return TypedResults.NoContent();
        }
        catch (Exception exception) when (IsConflict(exception))
        {
            return Conflict();
        }
    }

    public async Task<IResult> DeleteAsync(Guid id, string? concurrencyStamp, CancellationToken token)
    {
        if (string.IsNullOrWhiteSpace(concurrencyStamp))
            return TypedResults.BadRequest(new { code = "concurrency_required" });

        // SCIM rows are retained provenance. There is intentionally no delete
        // operation: disabling/revoking is the supported lifecycle action.
        var item = await dbContext.ScimConnections.AsNoTracking()
            .SingleOrDefaultAsync(connection => connection.Id == id, token);
        if (item is null) return TypedResults.NotFound();
        return Matches(item.ConcurrencyStamp, concurrencyStamp) ?
            TypedResults.Conflict(new { code = "provenance_conflict", message = "SCIM connections and their history cannot be deleted." }) :
            TypedResults.Conflict(new { code = "scim_conflict", message = "The SCIM state changed concurrently." });
    }

    public async Task<IResult> ListUsersAsync(Guid id, CancellationToken token) => TypedResults.Ok(await dbContext.ScimUserMappings.AsNoTracking().Where(item => item.ScimConnectionId == id).ToArrayAsync(token));

    public async Task<IResult> SetOverrideAsync(Guid id, Guid userId, ScimOverrideRequest request, CancellationToken token)
    {
        if (request.Override is not null && !Enum.IsDefined(typeof(ScimLifecycleOverride), request.Override.Value)) return TypedResults.BadRequest(new { code = "invalid_override" });
        var normalizedReason = NormalizeOverrideReason(request.Reason);
        if (normalizedReason is null) return TypedResults.BadRequest(new { code = "invalid_reason" });
        await using var transaction = await dbContext.Database.BeginTransactionAsync(token);
        try
        {
            var mapping = await dbContext.ScimUserMappings.SingleOrDefaultAsync(item => item.ScimConnectionId == id && item.UserId == userId, token);
            if (mapping is null) return TypedResults.NotFound();
            if (!Matches(mapping.ETag, request.ETag)) return Conflict();
            if (!await lifecycleService.IsScimControllableUserAsync(userId, token)) return TypedResults.BadRequest(new { code = "owner_protected" });

            var before = new
            {
                ScimConnectionId = id,
                UserId = userId,
                LifecycleOverride = mapping.LifecycleOverride,
                LifecycleOverrideReason = mapping.LifecycleOverrideReason,
                ETag = mapping.ETag,
            };
            var now = Clock.GetUtcNow();
            mapping.LifecycleOverride = request.Override;
            mapping.LifecycleOverrideReason = normalizedReason;
            mapping.UpdatedAt = now;
            mapping.Version++;
            mapping.ETag = NewStamp();
            if (OverrideBeforeSaveAsync is not null)
                await OverrideBeforeSaveAsync(token);
            await dbContext.SaveChangesAsync(token);
            var after = new
            {
                ScimConnectionId = id,
                UserId = userId,
                LifecycleOverride = mapping.LifecycleOverride,
                LifecycleOverrideReason = mapping.LifecycleOverrideReason,
                ETag = mapping.ETag,
            };
            await AuditAsync("scim.user.lifecycle-override", before, after, token);
            await transaction.CommitAsync(token);
            return TypedResults.Ok(mapping);
        }
        catch (DbUpdateConcurrencyException)
        {
            return Conflict();
        }
        catch (Exception exception) when (IsConflict(exception))
        {
            return Conflict();
        }
    }

    private async Task AuditAsync(string action, object? before, object after, CancellationToken cancellationToken)
    {
        var context = httpContextAccessor.HttpContext ?? throw new InvalidOperationException("An HTTP context is required for a SCIM control-plane audit event.");
        var actor = Guid.TryParse(context.User.FindFirst(System.Security.Claims.ClaimTypes.NameIdentifier)?.Value, out var actorId)
            ? actorId : (Guid?)null;
        await auditWriter.WriteAsync(dbContext, context, actor, null, null, action, before ?? new { }, after, cancellationToken);
    }

    private static IResult Conflict() => TypedResults.Conflict(new
    {
        code = "scim_conflict",
        message = "The SCIM state changed concurrently.",
    });

    private static bool Matches(string actual, string? expected) =>
        !string.IsNullOrEmpty(expected) && string.Equals(actual, expected, StringComparison.Ordinal);

    private static string NewStamp() => Convert.ToHexString(RandomNumberGenerator.GetBytes(16)).ToLowerInvariant();

    private static bool IsConflict(Exception exception)
    {
        for (var current = exception; current is not null; current = current.InnerException)
        {
            if (current is DbUpdateConcurrencyException) return true;
            if (current is PostgresException postgres && postgres.SqlState is
                PostgresErrorCodes.UniqueViolation or PostgresErrorCodes.SerializationFailure or PostgresErrorCodes.DeadlockDetected)
                return true;
        }

        return false;
    }

    private static string? NormalizeOverrideReason(string? value)
    {
        if (value is null) return null;

        var builder = new System.Text.StringBuilder(value.Length);
        var pendingWhitespace = false;
        foreach (var character in value)
        {
            if (char.IsControl(character)) return null;
            if (char.IsWhiteSpace(character))
            {
                pendingWhitespace = builder.Length > 0;
                continue;
            }

            if (pendingWhitespace) builder.Append(' ');
            builder.Append(character);
            pendingWhitespace = false;
        }

        var normalized = builder.ToString();
        return normalized.Length > 0 && normalized.Length <= 500 ? normalized : null;
    }
}