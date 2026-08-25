namespace Vantigo.Tenancy.EntityFramework;

/// <summary>
/// Implemented by DbContexts that own tenant-scoped entities. The tenant query
/// filter reads <see cref="CurrentTenantId"/> through the context instance,
/// which is the only filter shape EF Core re-evaluates (and parameterizes) per
/// query. A filter that captures any other object is compiled into the cached
/// query plan as a constant, silently pinning every later query of the same
/// shape to the first tenant that ran it.
/// </summary>
public interface ITenantDbContext
{
    /// <summary>
    /// The active tenant for this context's queries. Throws when no tenant is
    /// resolved, so tenant-filtered queries fail closed.
    /// </summary>
    Guid CurrentTenantId { get; }
}