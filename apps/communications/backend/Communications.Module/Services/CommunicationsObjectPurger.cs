using System.Runtime.ExceptionServices;

using Microsoft.EntityFrameworkCore;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Infrastructure.Storage;
using Vantigo.Storage.Abstractions;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Communications.Services;

public interface ICommunicationsObjectPurger
{
    Task PurgeAsync(CancellationToken cancellationToken = default);
}

/// <summary>Deletes only objects referenced by Communications metadata.</summary>
public sealed class CommunicationsObjectPurger(
    CommunicationsDbContext db,
    ITenantDirectory tenantDirectory,
    ILogger<CommunicationsObjectPurger> logger,
    IObjectStore<CommunicationsStorageScope> objectStore) : ICommunicationsObjectPurger
{
    public async Task PurgeAsync(CancellationToken cancellationToken = default)
    {
        // System-context discovery; tenant scope is entered before processing.
        Exception? firstFailure = null;
        foreach (var tenant in await tenantDirectory.GetActiveTenantsAsync(cancellationToken))
        {
            try
            {
                using var tenantScope = AmbientTenantContext.Enter(tenant);
                db.ChangeTracker.Clear();
                var keys = new HashSet<string>(StringComparer.Ordinal);
                keys.UnionWith(await db.ConversationMessages.AsNoTracking().Where(item => item.RawPayloadStorageKey != null).Select(item => item.RawPayloadStorageKey!).ToListAsync(cancellationToken));
                keys.UnionWith(await db.MessageAttachments.AsNoTracking().Select(item => item.StorageKey).ToListAsync(cancellationToken));
                keys.UnionWith(await db.AttachmentUploads.AsNoTracking().Select(item => item.StorageKey).ToListAsync(cancellationToken));
                keys.UnionWith(await db.InboundEmailJobs.AsNoTracking().Select(item => item.RawMimeStorageKey).ToListAsync(cancellationToken));
                keys.UnionWith(await db.AttachmentCleanupRecords.AsNoTracking().Select(item => item.StorageKey).ToListAsync(cancellationToken));

                foreach (var key in keys.Where(item => !string.IsNullOrWhiteSpace(item)))
                {
                    ObjectOwnershipLifecycle.EnsureSafeKey(key);
                    // Provider DeleteAsync is intentionally used instead of enumeration:
                    // missing objects are successful and unrelated scope keys are invisible.
                    await objectStore.DeleteAsync(key, cancellationToken);
                }
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested) { throw; }
            catch (Exception exception) when (!cancellationToken.IsCancellationRequested)
            {
                firstFailure ??= exception;
                logger.LogError(exception, "Communications object purge failed for tenant {TenantId}; continuing with the next tenant.", tenant.Value);
            }
        }

        if (firstFailure is not null)
            ExceptionDispatchInfo.Capture(firstFailure).Throw();
    }
}