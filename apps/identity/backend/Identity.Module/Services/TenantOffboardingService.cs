using System.Security.Cryptography;

using Microsoft.EntityFrameworkCore;

using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Services;

/// <summary>
/// Persists the control-plane hand-off for tenant offboarding. This remains a
/// safe deferred stub: it never deletes tenant data or starts orchestration.
/// </summary>
public sealed class TenantOffboardingService(AccountsDbContext dbContext)
{
    public async Task<(OffboardingState State, bool Created)> RequestExportAsync(
        Guid tenantId,
        CancellationToken cancellationToken = default)
    {
        var existing = await dbContext.TenantOffboardingStates
            .AsNoTracking()
            .SingleOrDefaultAsync(state => state.TenantId == tenantId, cancellationToken);
        if (existing is not null)
            return (ToState(existing), false);

        var purgeToken = Convert.ToHexString(RandomNumberGenerator.GetBytes(32));
        var state = new TenantOffboardingState
        {
            TenantId = tenantId,
            ExportId = Guid.NewGuid(),
            // Retained to preserve the existing idempotent export response
            // contract, which returns the token on repeated requests.
            PurgeToken = purgeToken,
            RequestedAtUtc = DateTimeOffset.UtcNow,
        };
        dbContext.TenantOffboardingStates.Add(state);
        try
        {
            await dbContext.SaveChangesAsync(cancellationToken);
            return (ToState(state), true);
        }
        catch (DbUpdateException)
        {
            dbContext.Entry(state).State = EntityState.Detached;
            var concurrent = await dbContext.TenantOffboardingStates
                .AsNoTracking()
                .SingleAsync(item => item.TenantId == tenantId, cancellationToken);
            return (ToState(concurrent), false);
        }
    }

    public async Task<OffboardingState?> GetAsync(Guid tenantId, CancellationToken cancellationToken = default)
    {
        var state = await dbContext.TenantOffboardingStates.AsNoTracking()
            .SingleOrDefaultAsync(item => item.TenantId == tenantId, cancellationToken);
        return state is null ? null : ToState(state);
    }

    public async Task<bool> ValidatePurgeAsync(
        Guid tenantId,
        Guid exportId,
        string token,
        CancellationToken cancellationToken = default)
    {
        var state = await dbContext.TenantOffboardingStates.AsNoTracking()
            .SingleOrDefaultAsync(item => item.TenantId == tenantId && item.ExportId == exportId, cancellationToken);
        if (state is null || string.IsNullOrWhiteSpace(token)) return false;

        return string.Equals(state.PurgeToken, token, StringComparison.Ordinal);
    }

    public async Task MarkPurgeRequestedAsync(Guid tenantId, CancellationToken cancellationToken = default)
    {
        var state = await dbContext.TenantOffboardingStates
            .SingleOrDefaultAsync(item => item.TenantId == tenantId, cancellationToken);
        if (state is null) return;
        state.PurgeRequestedAtUtc = DateTimeOffset.UtcNow;
        await dbContext.SaveChangesAsync(cancellationToken);
    }

    public async Task RemoveAsync(Guid tenantId, Guid exportId, CancellationToken cancellationToken = default)
    {
        var state = await dbContext.TenantOffboardingStates
            .SingleOrDefaultAsync(item => item.TenantId == tenantId && item.ExportId == exportId, cancellationToken);
        if (state is null) return;
        dbContext.TenantOffboardingStates.Remove(state);
        await dbContext.SaveChangesAsync(cancellationToken);
    }

    private static OffboardingState ToState(TenantOffboardingState state) => new(
        state.ExportId,
        state.TenantId,
        state.PurgeToken,
        state.RequestedAtUtc,
        state.PurgeRequestedAtUtc);
}

public sealed record OffboardingState(
    Guid ExportId,
    Guid TenantId,
    string PurgeToken,
    DateTimeOffset RequestedAtUtc,
    DateTimeOffset? PurgeRequestedAtUtc);