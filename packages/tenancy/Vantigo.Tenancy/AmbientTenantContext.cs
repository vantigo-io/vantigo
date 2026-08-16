using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Tenancy;

/// <summary>
/// AsyncLocal-backed tenant context. Flows across awaits in both HTTP request
/// pipelines and background job iterations.
/// </summary>
public sealed class AmbientTenantContext : ITenantContext
{
    private static readonly AsyncLocal<TenantId?> Ambient = new();

    public bool IsResolved => Ambient.Value is { IsEmpty: false };

    public TenantId Current => Ambient.Value is { IsEmpty: false } tenant
        ? tenant
        : throw new TenantUnresolvedException();

    /// <summary>
    /// Enters a tenant scope for the current execution flow. Dispose to restore
    /// the previous value. Used by resolution middleware and background workers.
    /// </summary>
    public static TenantScope Enter(TenantId tenantId)
    {
        if (tenantId.IsEmpty)
            throw new ArgumentException("Tenant id must not be empty.", nameof(tenantId));

        var previous = Ambient.Value;
        Ambient.Value = tenantId;
        return new TenantScope(previous);
    }

    public readonly struct TenantScope(TenantId? previous) : IDisposable
    {
        public void Dispose() => Ambient.Value = previous;
    }
}