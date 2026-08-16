using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Diagnostics;

using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Tenancy.EntityFramework;

/// <summary>
/// Stamps newly added tenant-owned entities and prevents cross-tenant writes.
/// </summary>
public sealed class TenantStampingInterceptor(ITenantContext tenantContext) : SaveChangesInterceptor
{
    /// <inheritdoc />
    public override InterceptionResult<int> SavingChanges(
        DbContextEventData eventData,
        InterceptionResult<int> result)
    {
        StampAndValidate(eventData.Context);
        return result;
    }

    /// <inheritdoc />
    public override ValueTask<InterceptionResult<int>> SavingChangesAsync(
        DbContextEventData eventData,
        InterceptionResult<int> result,
        CancellationToken cancellationToken = default)
    {
        StampAndValidate(eventData.Context);
        return ValueTask.FromResult(result);
    }

    private void StampAndValidate(DbContext? dbContext)
    {
        if (dbContext is null)
            return;

        var currentTenantId = tenantContext.Current.Value;

        foreach (var entry in dbContext.ChangeTracker.Entries())
        {
            if (entry.Entity is not ITenantOwned tenantOwned)
                continue;

            switch (entry.State)
            {
                case EntityState.Added:
                    if (tenantOwned.TenantId == Guid.Empty)
                    {
                        tenantOwned.TenantId = currentTenantId;
                    }
                    else if (tenantOwned.TenantId != currentTenantId)
                    {
                        throw new InvalidOperationException(
                            $"Cannot add a tenant-owned entity for tenant '{tenantOwned.TenantId}' while tenant '{currentTenantId}' is active.");
                    }

                    break;

                case EntityState.Modified:
                case EntityState.Deleted:
                    if (tenantOwned.TenantId != currentTenantId)
                    {
                        throw new InvalidOperationException(
                            $"Cannot {entry.State.ToString().ToLowerInvariant()} a tenant-owned entity belonging to tenant '{tenantOwned.TenantId}' while tenant '{currentTenantId}' is active.");
                    }

                    break;
            }
        }
    }
}