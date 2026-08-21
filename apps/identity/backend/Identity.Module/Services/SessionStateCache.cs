using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.ChangeTracking;
using Microsoft.EntityFrameworkCore.Diagnostics;
using Microsoft.Extensions.Caching.Memory;

using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Services;

/// <summary>
/// Short-lived per-user cache of the account state that cookie validation checks.
/// It exists so the validation path is not a database round trip per request; it is
/// deliberately small, in-process, and always safe to miss.
/// </summary>
public sealed class SessionStateCache(IMemoryCache cache)
{
    private const string KeyPrefix = "identity-session-state:";

    public AccountSessionState? Get(Guid userId) =>
        cache.TryGetValue(Key(userId), out AccountSessionState? state) ? state : null;

    public void Set(Guid userId, AccountSessionState state, TimeSpan duration)
    {
        if (duration <= TimeSpan.Zero)
        {
            Invalidate(userId);
            return;
        }

        cache.Set(Key(userId), state, duration);
    }

    public void Invalidate(Guid userId) => cache.Remove(Key(userId));

    private static string Key(Guid userId) => $"{KeyPrefix}{userId:D}";
}

/// <summary>
/// Drops the cached state of every account touched by a save. Password changes, MFA
/// changes, disablement, role changes, and explicit revocation all persist the user
/// row, so intercepting the save is what makes those take effect on the next
/// request instead of when the cache entry happens to expire. In a multi-instance
/// deployment this only covers the instance that performed the write; the other
/// instances converge within the configured cache duration.
/// </summary>
internal sealed class SessionStateInvalidationInterceptor(SessionStateCache cache) : SaveChangesInterceptor
{
    public override int SavedChanges(SaveChangesCompletedEventData eventData, int result)
    {
        Invalidate(eventData);
        return base.SavedChanges(eventData, result);
    }

    public override ValueTask<int> SavedChangesAsync(
        SaveChangesCompletedEventData eventData,
        int result,
        CancellationToken cancellationToken = default)
    {
        Invalidate(eventData);
        return base.SavedChangesAsync(eventData, result, cancellationToken);
    }

    private void Invalidate(SaveChangesCompletedEventData eventData)
    {
        DbContext? context = eventData.Context;
        if (context is null)
        {
            return;
        }

        foreach (EntityEntry<ApplicationUser> entry in context.ChangeTracker.Entries<ApplicationUser>())
        {
            cache.Invalidate(entry.Entity.Id);
        }
    }
}