using Microsoft.EntityFrameworkCore;

namespace Vantigo.Tenancy.EntityFramework;

/// <summary>
/// Allocates tenant-local, monotonically increasing counter values.
/// </summary>
public interface ITenantCounterService
{
    /// <summary>
    /// Returns the next value for a tenant and counter name. When called inside
    /// the caller's transaction, allocation is gapless for committed values.
    /// </summary>
    Task<long> NextAsync(
        DbContext db,
        string counterName,
        CancellationToken cancellationToken = default);
}