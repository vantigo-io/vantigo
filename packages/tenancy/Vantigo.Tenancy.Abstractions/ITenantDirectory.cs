namespace Vantigo.Tenancy.Abstractions;

/// <summary>
/// Looks up tenants from the control plane. Implemented by the identity module,
/// consumed by tenant resolution middleware and background workers.
/// </summary>
public interface ITenantDirectory
{
    /// <summary>Finds an active tenant by its URL slug, or null.</summary>
    Task<TenantId?> FindActiveBySlugAsync(string slug, CancellationToken cancellationToken = default);

    /// <summary>Whether the user is a member of the tenant.</summary>
    Task<bool> IsMemberAsync(Guid userId, TenantId tenantId, CancellationToken cancellationToken = default);

    /// <summary>The single-mode default tenant, provisioned at startup.</summary>
    Task<TenantId> GetDefaultTenantAsync(CancellationToken cancellationToken = default);

    /// <summary>All active tenant ids, for system-context iteration (cleanup, retention).</summary>
    Task<IReadOnlyList<TenantId>> GetActiveTenantsAsync(CancellationToken cancellationToken = default);

    /// <summary>Gets the canonical module keys enabled for an active tenant.</summary>
    Task<IReadOnlySet<string>> GetEnabledModulesAsync(
        TenantId tenantId,
        CancellationToken cancellationToken = default);
}