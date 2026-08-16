namespace Vantigo.Tenancy.Abstractions;

/// <summary>
/// Marks an entity as owned by a single tenant. Tenant-owned entities are
/// automatically stamped on insert and filtered on read.
/// </summary>
public interface ITenantOwned
{
    Guid TenantId { get; set; }
}