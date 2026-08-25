using Microsoft.EntityFrameworkCore;

using Npgsql;

using Vantigo.Configuration;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Services;

/// <summary>
/// Materializes one deterministic static SCIM connection so protocol mappings,
/// groups, and lifecycle state remain durable without a provider relationship.
/// Static SCIM is the only provisioning scope in this phase.
/// </summary>
public sealed class StaticScimStateInitializer(
    AccountsDbContext dbContext,
    StaticScimOptions options)
{
    private const int MaxEnsureAttempts = 4;

    /// <summary>
    /// Concurrently starting replicas race the check-then-insert on the fixed
    /// connection id; the loser's duplicate insert is retried and finds the
    /// row a sibling replica just created.
    /// </summary>
    public async Task EnsureAsync(CancellationToken cancellationToken = default)
    {
        if (!options.Enabled) return;

        for (var attempt = 1; ; attempt++)
        {
            try
            {
                await EnsureCoreAsync(cancellationToken);
                return;
            }
            catch (Exception exception) when (IsRetryableConflict(exception) && attempt < MaxEnsureAttempts)
            {
                dbContext.ChangeTracker.Clear();
                await Task.Delay(TimeSpan.FromMilliseconds(25 * attempt), cancellationToken);
            }
        }
    }

    private async Task EnsureCoreAsync(CancellationToken cancellationToken)
    {
        var now = DateTimeOffset.UtcNow;
        var connection = await dbContext.ScimConnections.SingleOrDefaultAsync(
            item => item.Id == ScimConnection.StaticId, cancellationToken);
        if (connection is null)
        {
            connection = new ScimConnection
            {
                Id = ScimConnection.StaticId,
                CreatedAt = now,
                UpdatedAt = now,
            };
            dbContext.ScimConnections.Add(connection);
        }
        await dbContext.SaveChangesAsync(cancellationToken);
    }

    private static bool IsRetryableConflict(Exception exception)
    {
        for (var current = exception; current is not null; current = current.InnerException)
        {
            if (current is PostgresException postgres && postgres.SqlState is
                PostgresErrorCodes.UniqueViolation or
                PostgresErrorCodes.SerializationFailure or
                PostgresErrorCodes.DeadlockDetected)
                return true;
        }

        return false;
    }
}