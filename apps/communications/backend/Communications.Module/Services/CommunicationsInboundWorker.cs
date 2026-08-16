using Microsoft.Extensions.Options;

namespace Vantigo.Communications.Services;

public sealed class CommunicationsInboundWorker(IServiceScopeFactory scopeFactory, ILogger<CommunicationsInboundWorker> logger, IOptions<MailgunInboundOptions> options) : BackgroundService
{
    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        var delay = TimeSpan.FromSeconds(Math.Max(1, options.Value.PollSeconds));
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                await using var scope = scopeFactory.CreateAsyncScope();
                var processor = scope.ServiceProvider.GetRequiredService<InboundEmailJobProcessor>();
                while (await processor.ProcessOneAsync(stoppingToken)) { }
            }
            catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested) { }
            catch (Exception exception) { logger.LogError(exception, "Communications inbound email processing failed."); }
            await Task.Delay(delay, stoppingToken);
        }
    }
}