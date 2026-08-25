using Microsoft.Extensions.Options;

using Npgsql;

using Vantigo.Configuration;

namespace Vantigo.Communications.Services;

public sealed class CommunicationsRetentionWorker(
    IServiceScopeFactory scopeFactory,
    NpgsqlDataSource dataSource,
    ILogger<CommunicationsRetentionWorker> logger,
    IOptions<CommunicationsOptions> options) : BackgroundService
{
    private readonly CommunicationsOptions communications = options.Value;

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        var minutes = Math.Max(1, communications.Retention.PollMinutes);
        var delay = TimeSpan.FromMinutes(minutes);
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                // The advisory lease keeps retention on exactly one replica per
                // cycle; the others skip and try again next interval.
                var ran = await CommunicationsAdvisoryLease.TryRunAsync(
                    dataSource,
                    CommunicationsAdvisoryLease.RetentionKey,
                    async () =>
                    {
                        await using var scope = scopeFactory.CreateAsyncScope();
                        var cleanup = scope.ServiceProvider.GetRequiredService<RetentionCleanupService>();
                        while (await cleanup.CleanupBatchAsync(DateTimeOffset.UtcNow, stoppingToken) > 0) { }
                    },
                    stoppingToken);
                if (!ran)
                    logger.LogDebug("Communications retention lease is held by another replica; skipping this cycle.");
            }
            catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested) { }
            catch (Exception exception) { logger.LogError(exception, "Communications retention cleanup failed."); }
            await Task.Delay(delay, stoppingToken);
        }
    }
}