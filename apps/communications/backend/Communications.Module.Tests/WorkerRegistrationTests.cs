using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;

using Vantigo.Communications.Database;

namespace Vantigo.Communications.Module.Tests;

/// <summary>
/// Workers:InProcess moves the background services out of the API replicas
/// (a dedicated `worker` process hosts them instead), so scaling HTTP does
/// not multiply pollers. The single-container default keeps them in-process.
/// </summary>
public sealed class WorkerRegistrationTests
{
    [Fact]
    public void Workers_are_hosted_in_process_by_default()
    {
        var services = new ServiceCollection();

        services.AddCommunicationsModule(new ConfigurationBuilder().Build());

        Assert.Equal(5, CommunicationsWorkerCount(services));
    }

    [Fact]
    public void Disabling_in_process_workers_removes_every_hosted_service()
    {
        var services = new ServiceCollection();
        var configuration = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?> { ["Workers:InProcess"] = "false" })
            .Build();

        services.AddCommunicationsModule(configuration);

        Assert.Equal(0, CommunicationsWorkerCount(services));
    }

    private static int CommunicationsWorkerCount(ServiceCollection services) =>
        services.Count(descriptor => descriptor.ServiceType == typeof(IHostedService) &&
            descriptor.ImplementationType?.Namespace?.StartsWith("Vantigo.Communications", StringComparison.Ordinal) == true);
}