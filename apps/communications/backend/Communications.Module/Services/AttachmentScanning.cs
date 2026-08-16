using System.Buffers.Binary;
using System.Net.Sockets;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Infrastructure.Storage;
using Vantigo.Storage.Abstractions;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Communications.Services;

public sealed class ClamAvOptions
{
    public string? Host { get; set; }
    public int Port { get; set; } = 3310;
    public int ConnectTimeoutSeconds { get; set; } = 3;
    public int ScanTimeoutSeconds { get; set; } = 30;
    public int MaxBytes { get; set; } = 10 * 1024 * 1024;
    public int MaxAttempts { get; set; } = 5;
    public int PollSeconds { get; set; } = 5;
}

public enum AttachmentScanVerdict
{
    Clean,
    Malware,
    Unavailable,
    TooLarge,
}

public sealed record AttachmentScanResult(AttachmentScanVerdict Verdict, string? Detail = null);

public interface IAttachmentScanner
{
    bool IsConfigured { get; }
    Task<AttachmentScanResult> ScanAsync(Stream content, long sizeBytes, CancellationToken cancellationToken = default);
}

internal sealed class DisabledAttachmentScanner : IAttachmentScanner
{
    public bool IsConfigured => false;
    public Task<AttachmentScanResult> ScanAsync(Stream content, long sizeBytes, CancellationToken cancellationToken = default) =>
        Task.FromResult(new AttachmentScanResult(AttachmentScanVerdict.Unavailable, "scanner_unavailable"));
}

/// <summary>Small ClamAV client using the documented zINSTREAM protocol.</summary>
public sealed class ClamAvAttachmentScanner(IOptions<ClamAvOptions> options, ILogger<ClamAvAttachmentScanner> logger) : IAttachmentScanner
{
    private readonly ClamAvOptions settings = options.Value;
    public bool IsConfigured => !string.IsNullOrWhiteSpace(settings.Host);

    public async Task<AttachmentScanResult> ScanAsync(Stream content, long sizeBytes, CancellationToken cancellationToken = default)
    {
        if (!IsConfigured) return new(AttachmentScanVerdict.Unavailable, "scanner_unavailable");
        if (sizeBytes > settings.MaxBytes) return new(AttachmentScanVerdict.TooLarge, "attachment_too_large");
        var (host, port) = ParseEndpoint(settings.Host!, settings.Port);
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeout.CancelAfter(TimeSpan.FromSeconds(Math.Clamp(settings.ScanTimeoutSeconds, 1, 300)));
        try
        {
            using var client = new TcpClient();
            using var connectTimeout = CancellationTokenSource.CreateLinkedTokenSource(timeout.Token);
            connectTimeout.CancelAfter(TimeSpan.FromSeconds(Math.Clamp(settings.ConnectTimeoutSeconds, 1, 30)));
            await client.ConnectAsync(host, port, connectTimeout.Token);
            await using var network = client.GetStream();
            await network.WriteAsync("zINSTREAM\0"u8.ToArray(), timeout.Token);
            var buffer = new byte[64 * 1024];
            var length = new byte[sizeof(int)];
            long total = 0;
            while (true)
            {
                var read = await content.ReadAsync(buffer, timeout.Token);
                if (read == 0) break;
                total += read;
                if (total > settings.MaxBytes) return new(AttachmentScanVerdict.TooLarge, "attachment_too_large");
                BinaryPrimitives.WriteInt32BigEndian(length, read);
                await network.WriteAsync(length, timeout.Token);
                await network.WriteAsync(buffer.AsMemory(0, read), timeout.Token);
            }
            await network.WriteAsync(new byte[4], timeout.Token);
            var response = new byte[4096];
            var count = await network.ReadAsync(response, timeout.Token);
            var result = System.Text.Encoding.ASCII.GetString(response, 0, count);
            if (result.Contains("FOUND", StringComparison.OrdinalIgnoreCase)) return new(AttachmentScanVerdict.Malware);
            if (result.Contains("OK", StringComparison.OrdinalIgnoreCase)) return new(AttachmentScanVerdict.Clean);
            logger.LogWarning("ClamAV returned an unrecognized response.");
            return new(AttachmentScanVerdict.Unavailable, "scanner_invalid_response");
        }
        catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
        {
            return new(AttachmentScanVerdict.Unavailable, "scanner_timeout");
        }
        catch (Exception exception) when (!cancellationToken.IsCancellationRequested)
        {
            logger.LogWarning(exception, "ClamAV attachment scan failed.");
            return new(AttachmentScanVerdict.Unavailable, "scanner_unavailable");
        }
    }

    private static (string Host, int Port) ParseEndpoint(string value, int fallbackPort)
    {
        if (Uri.TryCreate(value, UriKind.Absolute, out var uri) && uri.Host.Length > 0)
            return (uri.Host, uri.Port > 0 ? uri.Port : fallbackPort);
        var separator = value.LastIndexOf(':');
        if (separator > 0 && int.TryParse(value[(separator + 1)..], out var port)) return (value[..separator], port);
        return (value, fallbackPort);
    }
}

internal sealed class AttachmentScanProcessor(
    CommunicationsDbContext db,
    ITenantDirectory tenantDirectory,
    IObjectStore<CommunicationsStorageScope> objectStore,
    IAttachmentScanner scanner,
    IOptions<ClamAvOptions> options,
    ILogger<AttachmentScanProcessor> logger)
{
    private readonly ClamAvOptions settings = options.Value;

    public async Task<bool> ProcessOneAsync(CancellationToken cancellationToken)
    {
        // System-context discovery; tenant scope is entered before processing.
        // Active tenants are the bounded authoritative work list; no tenant-owned
        // table is scanned while the context is unresolved.
        foreach (var tenant in await tenantDirectory.GetActiveTenantsAsync(cancellationToken))
        {
            try
            {
                db.ChangeTracker.Clear();
                using var tenantScope = AmbientTenantContext.Enter(tenant);
                if (await ProcessOneForTenantAsync(tenant, cancellationToken)) return true;
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested) { throw; }
            catch (Exception exception)
            {
                logger.LogError(exception, "Communications attachment scan failed for tenant {TenantId}; continuing with the next tenant.", tenant.Value);
            }
            finally { db.ChangeTracker.Clear(); }
        }

        return false;
    }

    private async Task<bool> ProcessOneForTenantAsync(TenantId tenant, CancellationToken cancellationToken)
    {
        var now = DateTimeOffset.UtcNow;
        var attachment = await db.MessageAttachments.AsNoTracking()
            .Where(item => (item.ScanStatus == "pending" || item.ScanStatus == "retry") && item.NextScanAt <= now ||
                           item.ScanStatus == "scanning" && item.ScanLeaseUntil < now)
            .OrderBy(item => item.NextScanAt).FirstOrDefaultAsync(cancellationToken);
        if (attachment is not null)
        {
            var lease = Guid.NewGuid().ToString("N");
            var claimed = await db.MessageAttachments.Where(item => item.Id == attachment.Id && item.TenantId == tenant.Value &&
                ((item.ScanStatus == "pending" || item.ScanStatus == "retry") && item.NextScanAt <= now || item.ScanStatus == "scanning" && item.ScanLeaseUntil < now))
                .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.ScanStatus, "scanning")
                    .SetProperty(item => item.ScanLeaseId, lease).SetProperty(item => item.ScanLeaseUntil, now.AddMinutes(2))
                    .SetProperty(item => item.ScanAttempts, item => item.ScanAttempts + 1), cancellationToken);
            if (claimed == 0) return true;
            await ScanMessageAttachmentAsync(attachment.Id, lease, attachment.ScanAttempts + 1, cancellationToken);
            return true;
        }

        var upload = await db.AttachmentUploads.AsNoTracking()
            .Where(item => item.ExpiresAt > now &&
                ((item.ScanStatus == "pending" || item.ScanStatus == "retry") && item.NextScanAt <= now ||
                 item.ScanStatus == "scanning" && item.ScanLeaseUntil < now))
            .OrderBy(item => item.NextScanAt).FirstOrDefaultAsync(cancellationToken);
        if (upload is null)
        {
            await ExpireUploadsAsync(tenant, now, cancellationToken);
            return false;
        }
        var uploadLease = Guid.NewGuid().ToString("N");
        var uploadClaimed = await db.AttachmentUploads.Where(item => item.Id == upload.Id && item.TenantId == tenant.Value &&
            item.ExpiresAt > now &&
            ((item.ScanStatus == "pending" || item.ScanStatus == "retry") && item.NextScanAt <= now || item.ScanStatus == "scanning" && item.ScanLeaseUntil < now))
            .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.ScanStatus, "scanning")
                .SetProperty(item => item.ScanLeaseId, uploadLease).SetProperty(item => item.ScanLeaseUntil, now.AddMinutes(2))
                .SetProperty(item => item.ScanAttempts, item => item.ScanAttempts + 1), cancellationToken);
        if (uploadClaimed > 0) await ScanUploadAsync(upload.Id, uploadLease, upload.ScanAttempts + 1, cancellationToken);
        return true;
    }

    private async Task ScanMessageAttachmentAsync(Guid id, string lease, int attempts, CancellationToken ct)
    {
        var item = await db.MessageAttachments.AsNoTracking().SingleAsync(value => value.Id == id, ct);
        var verdict = await ScanObjectAsync(item.StorageKey, item.SizeBytes, ct);
        await ApplyVerdictAsync(id, lease, attempts, item.StorageKey, item.MessageId, verdict, false, ct);
    }

    private async Task ScanUploadAsync(Guid id, string lease, int attempts, CancellationToken ct)
    {
        var item = await db.AttachmentUploads.AsNoTracking().SingleAsync(value => value.Id == id, ct);
        var verdict = await ScanObjectAsync(item.StorageKey, item.SizeBytes, ct);
        await ApplyVerdictAsync(id, lease, attempts, item.StorageKey, null, verdict, true, ct);
    }

    private async Task<AttachmentScanResult> ScanObjectAsync(string storageKey, long sizeBytes, CancellationToken ct)
    {
        try
        {
            await using var content = await objectStore.GetAsync(storageKey, ct) ?? throw new InvalidOperationException("Attachment object is unavailable.");
            return await scanner.ScanAsync(content, sizeBytes, ct);
        }
        catch (Exception exception) when (!ct.IsCancellationRequested)
        {
            logger.LogWarning(exception, "Attachment scan failed.");
            return new(AttachmentScanVerdict.Unavailable, "scanner_unavailable");
        }
    }

    private async Task ApplyVerdictAsync(Guid id, string lease, int attempts, string storageKey, Guid? messageId,
        AttachmentScanResult verdict, bool upload, CancellationToken ct)
    {
        var now = DateTimeOffset.UtcNow;
        var disabled = !scanner.IsConfigured && verdict.Verdict == AttachmentScanVerdict.Unavailable;
        var terminal = verdict.Verdict is AttachmentScanVerdict.Malware or AttachmentScanVerdict.TooLarge ||
                       verdict.Verdict == AttachmentScanVerdict.Unavailable && !disabled && attempts >= Math.Max(1, settings.MaxAttempts);
        var next = terminal ? now : now.AddSeconds(Math.Min(3600, Math.Pow(2, Math.Min(attempts, 10))));
        var status = verdict.Verdict == AttachmentScanVerdict.Clean ? "clean" : terminal
            ? verdict.Verdict is AttachmentScanVerdict.Malware or AttachmentScanVerdict.TooLarge ? "quarantined" : "failed"
            : "pending";
        var error = status == "clean" ? null : status == "quarantined" ? "Attachment was rejected." : "Attachment scanning failed.";

        await using var transaction = await db.Database.BeginTransactionAsync(ct);
        var updated = upload
            ? await db.AttachmentUploads.Where(item => item.Id == id && item.ScanStatus == "scanning" && item.ScanLeaseId == lease)
                .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.ScanStatus, status)
                    .SetProperty(item => item.ScanError, error).SetProperty(item => item.NextScanAt, next)
                    .SetProperty(item => item.ScanLeaseId, (string?)null).SetProperty(item => item.ScanLeaseUntil, (DateTimeOffset?)null), ct)
            : await db.MessageAttachments.Where(item => item.Id == id && item.ScanStatus == "scanning" && item.ScanLeaseId == lease)
                .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.ScanStatus, status)
                    .SetProperty(item => item.ScanError, error).SetProperty(item => item.NextScanAt, next)
                    .SetProperty(item => item.ScanLeaseId, (string?)null).SetProperty(item => item.ScanLeaseUntil, (DateTimeOffset?)null), ct);
        if (updated == 0)
        {
            await transaction.RollbackAsync(ct);
            return;
        }

        if (terminal)
            await ObjectOwnershipLifecycle.QueueForDeletionAsync(db, storageKey, messageId, now, ct);
        await db.SaveChangesAsync(ct);
        await transaction.CommitAsync(ct);
    }

    private async Task ExpireUploadsAsync(TenantId tenant, DateTimeOffset now, CancellationToken ct)
    {
        var expired = await db.AttachmentUploads.AsNoTracking()
            .Where(item => item.ExpiresAt < now && item.ScanStatus != "quarantined" &&
                item.ScanStatus != "expired" && item.ScanStatus != "claimed" &&
                (item.ScanStatus != "scanning" || item.ScanLeaseUntil <= now))
            .OrderBy(item => item.ExpiresAt).Take(100)
            .Select(item => new { item.Id, item.StorageKey })
            .ToListAsync(ct);
        if (expired.Count == 0) return;

        await using var transaction = await db.Database.BeginTransactionAsync(ct);
        foreach (var item in expired)
        {
            // The conditional transition is the expiry claim. A concurrent reply
            // can win with the clean -> claimed transition, in which case this
            // worker must not enqueue that object's deletion.
            var transitioned = await db.AttachmentUploads
                .Where(upload => upload.Id == item.Id && upload.TenantId == tenant.Value && upload.ExpiresAt < now &&
                    upload.ScanStatus != "quarantined" && upload.ScanStatus != "expired" && upload.ScanStatus != "claimed" &&
                    (upload.ScanStatus != "scanning" || upload.ScanLeaseUntil <= now))
                .ExecuteUpdateAsync(setters => setters.SetProperty(upload => upload.ScanStatus, "expired")
                    .SetProperty(upload => upload.ScanLeaseId, (string?)null)
                    .SetProperty(upload => upload.ScanLeaseUntil, (DateTimeOffset?)null), ct);
            if (transitioned == 1)
                await ObjectOwnershipLifecycle.QueueForDeletionAsync(db, item.StorageKey, null, now, ct);
        }
        await db.SaveChangesAsync(ct);
        await transaction.CommitAsync(ct);
    }
}

public sealed class CommunicationsAttachmentScannerWorker(IServiceScopeFactory scopeFactory, ILogger<CommunicationsAttachmentScannerWorker> logger, IOptions<ClamAvOptions> options) : BackgroundService
{
    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                await using var scope = scopeFactory.CreateAsyncScope();
                var processor = scope.ServiceProvider.GetRequiredService<AttachmentScanProcessor>();
                while (await processor.ProcessOneAsync(stoppingToken)) { }
            }
            catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested) { }
            catch (Exception exception) { logger.LogError(exception, "Communications attachment scanning failed."); }
            await Task.Delay(TimeSpan.FromSeconds(Math.Max(1, options.Value.PollSeconds)), stoppingToken);
        }
    }
}