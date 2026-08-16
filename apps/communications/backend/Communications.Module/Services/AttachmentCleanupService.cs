using Microsoft.EntityFrameworkCore;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Infrastructure.Storage;
using Vantigo.Storage.Abstractions;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Communications.Services;

public sealed class AttachmentCleanupService(
    CommunicationsDbContext db,
    ITenantDirectory tenantDirectory,
    ILogger<AttachmentCleanupService> logger,
    IObjectStore<CommunicationsStorageScope> objectStore)
{
    public async Task<int> CleanupBatchAsync(DateTimeOffset now, CancellationToken cancellationToken)
    {
        var cleaned = 0;
        // System-context discovery; tenant scope is entered before processing.
        // Cleanup is intentionally iterated from the active tenant directory.
        foreach (var tenant in await tenantDirectory.GetActiveTenantsAsync(cancellationToken))
        {
            try
            {
                db.ChangeTracker.Clear();
                using var tenantScope = AmbientTenantContext.Enter(tenant);
                cleaned += await CleanupTenantBatchAsync(now, cancellationToken);
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested) { throw; }
            catch (Exception exception)
            {
                logger.LogError(exception, "Communications object cleanup failed for tenant {TenantId}.", tenant.Value);
            }
            finally { db.ChangeTracker.Clear(); }
        }

        return cleaned;
    }

    private async Task<int> CleanupTenantBatchAsync(DateTimeOffset now, CancellationToken cancellationToken)
    {

        var candidates = await db.AttachmentCleanupRecords
            .Where(item => (item.Status == ObjectOwnershipLifecycle.Pending && item.NextAttemptAt <= now) ||
                (item.Status == ObjectOwnershipLifecycle.Staged && item.ReservationExpiresAt <= now) ||
                (item.Status == ObjectOwnershipLifecycle.Deleting && item.LeaseUntil <= now))
            .OrderBy(item => item.CreatedAt).Take(100).Select(item => item.Id).ToListAsync(cancellationToken);
        var records = new List<AttachmentCleanupRecord>();
        foreach (var id in candidates)
        {
            var lease = Guid.NewGuid().ToString("N");
            var claimed = await db.AttachmentCleanupRecords.Where(item => item.Id == id &&
                ((item.Status == ObjectOwnershipLifecycle.Pending && item.NextAttemptAt <= now) ||
                 (item.Status == ObjectOwnershipLifecycle.Staged && item.ReservationExpiresAt <= now) ||
                 (item.Status == ObjectOwnershipLifecycle.Deleting && item.LeaseUntil <= now)))
                .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.Status, ObjectOwnershipLifecycle.Deleting)
                    .SetProperty(item => item.LeaseId, lease).SetProperty(item => item.LeaseUntil, now.AddMinutes(5)), cancellationToken);
            if (claimed == 0) continue;
            var record = await db.AttachmentCleanupRecords.SingleAsync(item => item.Id == id, cancellationToken);
            records.Add(record);
        }
        foreach (var record in records)
        {
            try
            {
                await objectStore.DeleteAsync(record.StorageKey, cancellationToken);
                record.Status = ObjectOwnershipLifecycle.Completed;
                record.LeaseId = null;
                record.LeaseUntil = null;
            }
            catch (Exception) when (!cancellationToken.IsCancellationRequested)
            {
                record.Status = ObjectOwnershipLifecycle.Pending;
                record.Attempts++;
                record.LastError = "Object cleanup failed.";
                record.NextAttemptAt = now.AddSeconds(Math.Min(3600, Math.Pow(2, Math.Min(record.Attempts, 10))));
                record.LeaseId = null;
                record.LeaseUntil = null;
            }
        }
        await db.SaveChangesAsync(cancellationToken);
        return records.Count;
    }
}