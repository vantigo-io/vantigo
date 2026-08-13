using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Services;

public static class OperationalEventKinds
{
    public const string StaticOidcSignInSucceeded = "static-oidc.sign-in-succeeded";
    public const string AuthenticatedScimRequest = "scim.authenticated-request";
}

public sealed class OperationalEventService(
    AccountsDbContext dbContext,
    IServiceScopeFactory scopeFactory)
{
    public async Task RecordAsync(string kind, DateTimeOffset occurredAt, CancellationToken cancellationToken)
    {
        // Use a fresh context and one provider-native atomic statement. This
        // avoids poisoning the request DbContext after a concurrent write and
        // makes the one-row-per-kind projection race-safe.
        await using var scope = scopeFactory.CreateAsyncScope();
        var isolatedDbContext = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        await isolatedDbContext.Database.ExecuteSqlInterpolatedAsync($"""
            INSERT INTO identity.operational_events (kind, occurred_at)
            VALUES ({kind}, {occurredAt})
            ON CONFLICT (kind) DO UPDATE SET occurred_at = EXCLUDED.occurred_at
            """, cancellationToken);
    }

    public Task<DateTimeOffset?> LastAsync(string kind, CancellationToken cancellationToken) =>
        dbContext.OperationalEvents.AsNoTracking()
            .Where(item => item.Kind == kind)
            .OrderByDescending(item => item.OccurredAt)
            .Select(item => (DateTimeOffset?)item.OccurredAt)
            .FirstOrDefaultAsync(cancellationToken);
}