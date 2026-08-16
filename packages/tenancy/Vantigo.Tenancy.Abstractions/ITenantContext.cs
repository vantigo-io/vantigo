namespace Vantigo.Tenancy.Abstractions;

/// <summary>
/// Provides the ambient tenant for the current unit of work (HTTP request or
/// background job iteration). Data access fails closed when unresolved.
/// </summary>
public interface ITenantContext
{
    /// <summary>Whether a tenant has been resolved for the current execution flow.</summary>
    bool IsResolved { get; }

    /// <summary>
    /// The resolved tenant.
    /// </summary>
    /// <exception cref="TenantUnresolvedException">No tenant is resolved.</exception>
    TenantId Current { get; }
}

/// <summary>Thrown when tenant-scoped work executes without a resolved tenant.</summary>
public sealed class TenantUnresolvedException()
    : InvalidOperationException("No tenant is resolved for the current execution flow.");