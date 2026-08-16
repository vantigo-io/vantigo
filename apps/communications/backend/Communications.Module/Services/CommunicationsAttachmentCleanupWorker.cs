namespace Vantigo.Communications.Services;

public sealed class CommunicationsAttachmentCleanupWorker(IServiceScopeFactory scopeFactory, ILogger<CommunicationsAttachmentCleanupWorker> logger) : BackgroundService
{
    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                await using var scope = scopeFactory.CreateAsyncScope();
                await scope.ServiceProvider.GetRequiredService<AttachmentCleanupService>().CleanupBatchAsync(DateTimeOffset.UtcNow, stoppingToken);
            }
            catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested) { }
            catch (Exception exception) { logger.LogError(exception, "Communications object cleanup failed."); }
            await Task.Delay(TimeSpan.FromMinutes(1), stoppingToken);
        }
    }
}