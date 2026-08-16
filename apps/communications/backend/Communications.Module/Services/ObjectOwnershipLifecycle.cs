using System.Security.Cryptography;
using System.Text;

using Microsoft.EntityFrameworkCore;

using Vantigo.Communications.Database.Communications;

namespace Vantigo.Communications.Services;

/// <summary>
/// Coordinates the small state machine that protects Communications objects while
/// the database and object store cannot participate in one transaction.
/// </summary>
internal static class ObjectOwnershipLifecycle
{
    internal const string Staged = "staged";
    internal const string Pending = "pending";
    internal const string Deleting = "deleting";
    internal const string Owned = "owned";
    internal const string Completed = "completed";

    private static readonly TimeSpan ReservationLifetime = TimeSpan.FromMinutes(10);

    internal static bool IsSafeRelativeKey(string key)
    {
        if (string.IsNullOrWhiteSpace(key) || key.StartsWith('/') || key.Contains('\\') || key.Any(char.IsControl) || Path.IsPathRooted(key))
            return false;

        return key.Split('/').All(part => part.Length > 0 && part is not "." and not "..");
    }

    internal static Guid DeterministicGuid(Guid seed, string discriminator)
    {
        var bytes = SHA256.HashData(Encoding.UTF8.GetBytes($"{seed:N}:{discriminator}"));
        return new Guid(bytes.AsSpan(0, 16));
    }

    internal static async Task<AttachmentCleanupRecord> ReserveAsync(
        CommunicationsDbContext db,
        string storageKey,
        DateTimeOffset now,
        CancellationToken cancellationToken)
    {
        EnsureSafeKey(storageKey);
        var record = await db.AttachmentCleanupRecords
            .Where(item => item.StorageKey == storageKey && item.Status != Completed)
            .OrderByDescending(item => item.CreatedAt)
            .FirstOrDefaultAsync(cancellationToken);

        if (record is null)
        {
            record = new AttachmentCleanupRecord
            {
                Id = DeterministicGuid(Guid.Empty, $"cleanup:{storageKey}"),
                StorageKey = storageKey,
                Status = Staged,
                NextAttemptAt = now,
                ReservationExpiresAt = now.Add(ReservationLifetime),
                CreatedAt = now,
            };
            // A deterministic id is useful for retries, but a completed historical
            // record can legitimately have the same key. In that case use a new row.
            if (await db.AttachmentCleanupRecords.AnyAsync(item => item.Id == record.Id, cancellationToken))
                record.Id = Guid.NewGuid();
            db.AttachmentCleanupRecords.Add(record);
        }
        else
        {
            if (record.Status == Deleting)
                throw new InvalidOperationException($"Object cleanup is already deleting reservation '{storageKey}'.");
            if (record.Status == Owned)
                throw new InvalidOperationException($"Object '{storageKey}' is already owned by Communications metadata.");

            record.Status = Staged;
            record.NextAttemptAt = now;
            record.ReservationExpiresAt = now.Add(ReservationLifetime);
            record.LastError = null;
            record.LeaseId = null;
            record.LeaseUntil = null;
        }

        return record;
    }

    /// <summary>Marks reservations owned in the same transaction as their metadata.</summary>
    internal static async Task MarkOwnedAsync(
        CommunicationsDbContext db,
        IEnumerable<string> storageKeys,
        CancellationToken cancellationToken)
    {
        var keys = storageKeys.Distinct(StringComparer.Ordinal).ToArray();
        foreach (var key in keys) EnsureSafeKey(key);
        if (keys.Length == 0) return;

        var records = await db.AttachmentCleanupRecords
            .Where(item => keys.Contains(item.StorageKey) && item.Status != Completed)
            .ToListAsync(cancellationToken);
        if (records.Any(item => item.Status == Deleting))
            throw new InvalidOperationException("An object is concurrently being deleted.");

        var recordsByKey = records.GroupBy(item => item.StorageKey).ToDictionary(item => item.Key, item => item.First(), StringComparer.Ordinal);
        var now = DateTimeOffset.UtcNow;
        foreach (var key in keys)
        {
            if (!recordsByKey.TryGetValue(key, out var record))
            {
                record = new AttachmentCleanupRecord
                {
                    Id = Guid.NewGuid(),
                    StorageKey = key,
                    CreatedAt = now,
                    NextAttemptAt = now,
                };
                db.AttachmentCleanupRecords.Add(record);
            }

            record.Status = Owned;
            record.ReservationExpiresAt = null;
            record.LastError = null;
            record.LeaseId = null;
            record.LeaseUntil = null;
        }
    }

    internal static async Task ReleaseAsync(CommunicationsDbContext db, string storageKey, CancellationToken cancellationToken)
    {
        EnsureSafeKey(storageKey);
        await db.AttachmentCleanupRecords
            .Where(item => item.StorageKey == storageKey && item.Status == Staged)
            .ExecuteUpdateAsync(setters => setters
                .SetProperty(item => item.Status, Pending)
                .SetProperty(item => item.ReservationExpiresAt, (DateTimeOffset?)null)
                .SetProperty(item => item.NextAttemptAt, DateTimeOffset.UtcNow)
                .SetProperty(item => item.LeaseId, (string?)null)
                .SetProperty(item => item.LeaseUntil, (DateTimeOffset?)null), cancellationToken);
    }

    /// <summary>Queues an owned object after its metadata is removed in the same transaction.</summary>
    internal static async Task QueueForDeletionAsync(
        CommunicationsDbContext db,
        string storageKey,
        Guid? messageId,
        DateTimeOffset now,
        CancellationToken cancellationToken)
    {
        EnsureSafeKey(storageKey);
        var record = await db.AttachmentCleanupRecords
            .Where(item => item.StorageKey == storageKey && item.Status != Completed)
            .OrderByDescending(item => item.CreatedAt)
            .FirstOrDefaultAsync(cancellationToken);
        if (record is null)
        {
            // A completed reservation means the object has already been deleted.
            // Do not create a second cleanup record for a later retention/expiry
            // poll. A new object with a reused key has a newer non-completed
            // reservation and is handled by the query above.
            var completed = await db.AttachmentCleanupRecords
                .AnyAsync(item => item.StorageKey == storageKey && item.Status == Completed, cancellationToken);
            if (completed) return;
        }
        if (record is null)
        {
            db.AttachmentCleanupRecords.Add(new AttachmentCleanupRecord
            {
                Id = Guid.NewGuid(),
                MessageId = messageId,
                StorageKey = storageKey,
                Status = Pending,
                CreatedAt = now,
                NextAttemptAt = now,
            });
            return;
        }

        if (record.Status == Deleting) return;
        record.MessageId = messageId;
        record.Status = Pending;
        record.NextAttemptAt = now;
        record.ReservationExpiresAt = null;
        record.LastError = null;
        record.LeaseId = null;
        record.LeaseUntil = null;
    }

    internal static void EnsureSafeKey(string storageKey)
    {
        if (!IsSafeRelativeKey(storageKey))
            throw new InvalidOperationException("Communications object keys must be safe relative keys.");
    }
}