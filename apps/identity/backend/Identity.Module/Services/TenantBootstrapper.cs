using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Services;

/// <summary>Performs the idempotent control-plane tenant bootstrap.</summary>
public sealed class TenantBootstrapper(AccountsDbContext dbContext, IOptions<TenancyOptions> tenancyOptions)
{
    /// <summary>Ensures the default tenant and single-mode memberships exist.</summary>
    public async Task EnsureAsync(CancellationToken cancellationToken = default)
    {
        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable, cancellationToken);

        var tenant = await dbContext.Tenants.SingleOrDefaultAsync(item => item.Slug == TenantSlug.Default, cancellationToken);
        if (tenant is null)
        {
            tenant = new Tenant
            {
                Name = "Default",
                Slug = TenantSlug.Default,
                Status = TenantStatus.Active,
                CreatedAtUtc = DateTimeOffset.UtcNow,
            };
            dbContext.Tenants.Add(tenant);
            await dbContext.SaveChangesAsync(cancellationToken);
        }

        if (!tenancyOptions.Value.IsMultiTenant)
        {
            var existingUserIds = await dbContext.TenantMemberships
                .Where(membership => membership.TenantId == tenant.Id)
                .Select(membership => membership.UserId)
                .ToHashSetAsync(cancellationToken);
            var users = await dbContext.Users.AsNoTracking().Select(user => user.Id).ToListAsync(cancellationToken);
            foreach (var userId in users.Where(id => !existingUserIds.Contains(id)))
            {
                dbContext.TenantMemberships.Add(new TenantMembership
                {
                    UserId = userId,
                    TenantId = tenant.Id,
                    CreatedAtUtc = DateTimeOffset.UtcNow,
                });
            }
            await dbContext.SaveChangesAsync(cancellationToken);
        }

        await transaction.CommitAsync(cancellationToken);
    }
}