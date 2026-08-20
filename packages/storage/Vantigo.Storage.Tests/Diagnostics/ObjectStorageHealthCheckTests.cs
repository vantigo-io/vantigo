using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Diagnostics.HealthChecks;
using Microsoft.Extensions.Hosting;

using Vantigo.Storage.Abstractions;
using Vantigo.Storage.Diagnostics;
using Vantigo.Storage.Initialization;
using Vantigo.Storage.Tests.TestSupport;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Storage.Tests.Diagnostics;

public sealed class ObjectStorageHealthCheckTests
{
    [Fact]
    public async Task Unconfigured_storage_is_skipped_and_reported_healthy()
    {
        var services = CreateServices();
        services.AddVantigoObjectStorage(StorageTestConfiguration.Build());
        services.AddHealthChecks().AddVantigoObjectStorageHealthCheck();
        using var provider = services.BuildServiceProvider();
        var healthCheckService = provider.GetRequiredService<HealthCheckService>();

        var report = await healthCheckService.CheckHealthAsync();

        Assert.Equal(HealthStatus.Healthy, report.Status);
        Assert.Contains("not configured", report.Entries["object_storage"].Description, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public async Task Reachable_local_backend_is_healthy()
    {
        var root = Path.Combine(Path.GetTempPath(), $"vantigo-health-{Guid.NewGuid():N}");
        try
        {
            var services = CreateServices();
            services.AddSingleton<IHostEnvironment>(new TestHostEnvironment());
            services.AddVantigoObjectStorage(StorageTestConfiguration.Build(
                ("Storage:Provider", "local"),
                ("Storage:Authentication", "none"),
                ("Storage:Local:RootPath", root)));
            services.AddHealthChecks().AddVantigoObjectStorageHealthCheck();
            using var provider = services.BuildServiceProvider();
            var healthCheckService = provider.GetRequiredService<HealthCheckService>();

            var report = await healthCheckService.CheckHealthAsync();

            Assert.Equal(HealthStatus.Healthy, report.Status);
        }
        finally
        {
            if (Directory.Exists(root)) Directory.Delete(root, recursive: true);
        }
    }

    [Fact]
    public async Task Unreachable_backend_is_reported_unhealthy()
    {
        var services = CreateServices();
        services.AddVantigoObjectStorage(StorageTestConfiguration.Build(
            ("Storage:Provider", "local"),
            ("Storage:Authentication", "none"),
            ("Storage:Local:RootPath", Path.Combine(Path.GetTempPath(), $"vantigo-health-{Guid.NewGuid():N}"))));
        services.AddSingleton<IStorageBackend>(new StorageBackendAdapter(new UnreachableObjectStore()));
        services.AddHealthChecks().AddVantigoObjectStorageHealthCheck();
        using var provider = services.BuildServiceProvider();
        var healthCheckService = provider.GetRequiredService<HealthCheckService>();

        var report = await healthCheckService.CheckHealthAsync();

        Assert.Equal(HealthStatus.Unhealthy, report.Status);
    }

    private static ServiceCollection CreateServices()
    {
        var services = new ServiceCollection();
        services.AddLogging();
        services.AddSingleton<ITenantContext>(new TestTenantContext(
            new TenantId(Guid.Parse("11111111-1111-1111-1111-111111111111"))));
        return services;
    }

    private sealed class UnreachableObjectStore : IObjectStore
    {
        public Task PutAsync(string key, Stream content, string contentType, CancellationToken cancellationToken = default) =>
            throw new InvalidOperationException("Object storage is unreachable.");

        public Task<Stream?> GetAsync(string key, CancellationToken cancellationToken = default) =>
            throw new InvalidOperationException("Object storage is unreachable.");

        public Task<bool> ExistsAsync(string key, CancellationToken cancellationToken = default) =>
            throw new InvalidOperationException("Object storage is unreachable.");

        public Task DeleteAsync(string key, CancellationToken cancellationToken = default) =>
            throw new InvalidOperationException("Object storage is unreachable.");
    }
}