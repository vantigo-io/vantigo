using Microsoft.EntityFrameworkCore;

using Vantigo.Identity.Database.Accounts;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Identity.Services;

/// <summary>Resolves active tenants from the identity control plane.</summary>
public sealed class TenantDirectory(AccountsDbContext dbContext) : ITenantDirectory
{
    private TenantId? cachedDefaultTenant;
    private readonly Dictionary<Guid, IReadOnlySet<string>> cachedEnabledModules = [];

    /// <inheritdoc />
    public async Task<TenantId?> FindActiveBySlugAsync(string slug, CancellationToken cancellationToken = default)
    {
        var normalizedSlug = TenantSlug.Normalize(slug);
        if (normalizedSlug is null) return null;

        var id = await dbContext.Tenants.AsNoTracking()
            .Where(tenant => tenant.Slug == normalizedSlug && tenant.Status == TenantStatus.Active)
            .Select(tenant => (Guid?)tenant.Id)
            .SingleOrDefaultAsync(cancellationToken);
        return id is Guid value ? new TenantId(value) : null;
    }

    /// <inheritdoc />
    public Task<bool> IsMemberAsync(Guid userId, TenantId tenantId, CancellationToken cancellationToken = default) =>
        dbContext.TenantMemberships.AsNoTracking().AnyAsync(membership =>
            membership.UserId == userId && membership.TenantId == tenantId.Value &&
            dbContext.Tenants.Any(tenant => tenant.Id == membership.TenantId && tenant.Status == TenantStatus.Active), cancellationToken);

    /// <inheritdoc />
    public async Task<TenantId> GetDefaultTenantAsync(CancellationToken cancellationToken = default)
    {
        if (cachedDefaultTenant is TenantId cached) return cached;

        var id = await dbContext.Tenants.AsNoTracking()
            .Where(tenant => tenant.Slug == TenantSlug.Default && tenant.Status == TenantStatus.Active)
            .Select(tenant => (Guid?)tenant.Id)
            .SingleOrDefaultAsync(cancellationToken);
        if (id is not Guid value)
        {
            // Startup normally provisions this row. This small recovery path is
            // also needed for hosts that recreate the schema during integration
            // tests or after a failed first migration.
            var tenant = new Tenant
            {
                Name = "Default",
                Slug = TenantSlug.Default,
                Status = TenantStatus.Active,
                CreatedAtUtc = DateTimeOffset.UtcNow,
            };
            dbContext.Tenants.Add(tenant);
            try
            {
                await dbContext.SaveChangesAsync(cancellationToken);
                value = tenant.Id;
            }
            catch (DbUpdateException)
            {
                dbContext.Entry(tenant).State = EntityState.Detached;
                value = await dbContext.Tenants.AsNoTracking()
                    .Where(item => item.Slug == TenantSlug.Default && item.Status == TenantStatus.Active)
                    .Select(item => (Guid?)item.Id)
                    .SingleOrDefaultAsync(cancellationToken)
                    ?? throw new InvalidOperationException("The default tenant could not be provisioned.");
            }
        }

        cachedDefaultTenant = new TenantId(value);
        return cachedDefaultTenant.Value;
    }

    /// <inheritdoc />
    public async Task<IReadOnlyList<TenantId>> GetActiveTenantsAsync(CancellationToken cancellationToken = default) =>
        (await dbContext.Tenants.AsNoTracking().Where(tenant => tenant.Status == TenantStatus.Active)
            .OrderBy(tenant => tenant.Id).Select(tenant => tenant.Id).ToListAsync(cancellationToken))
        .Select(id => new TenantId(id)).ToArray();

    /// <inheritdoc />
    public async Task<IReadOnlySet<string>> GetEnabledModulesAsync(
        TenantId tenantId,
        CancellationToken cancellationToken = default)
    {
        if (cachedEnabledModules.TryGetValue(tenantId.Value, out var cached)) return cached;

        var modules = await dbContext.Tenants.AsNoTracking()
            .Where(tenant => tenant.Id == tenantId.Value && tenant.Status == TenantStatus.Active)
            .Select(tenant => tenant.EnabledModules)
            .SingleOrDefaultAsync(cancellationToken);
        var result = (modules ?? []).ToHashSet(StringComparer.Ordinal);
        cachedEnabledModules[tenantId.Value] = result;
        return result;
    }
}

/// <summary>Validates and normalizes tenant slugs at the application boundary.</summary>
public static class TenantSlug
{
    public const string Default = "default";

    public static string? Normalize(string? value)
    {
        if (string.IsNullOrWhiteSpace(value)) return null;
        var slug = value.Trim().ToLowerInvariant();
        if (slug.Length > 63 || slug.Any(character =>
                !(char.IsAsciiLetterOrDigit(character) || character is '-' or '_')) ||
            slug[0] is '-' or '_' || slug[^1] is '-' or '_')
            return null;
        return slug;
    }
}