using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;

using Testcontainers.PostgreSql;

namespace Vantigo.Customers.Api.Tests.Integration;

/// <summary>
/// Boots the Customers API against a real PostgreSQL instance running in a
/// Testcontainers-managed Docker container. The container is shared across all
/// tests in the <see cref="CustomersApiCollection"/> to keep the test run fast.
/// Migrations are applied by the API itself on startup. Outbound calls to
/// Brønnøysundregisteret are routed to <see cref="BrregHandler"/> instead of the
/// real registry.
/// </summary>
public sealed class CustomersApiFactory : WebApplicationFactory<Program>, IAsyncLifetime
{
    private readonly PostgreSqlContainer _postgres = new PostgreSqlBuilder("postgres:17-alpine")
        .WithDatabase("customers")
        .Build();

    public StubBrregHandler BrregHandler { get; } = new();

    public async Task InitializeAsync()
    {
        await _postgres.StartAsync();
    }

    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        builder.UseEnvironment(Environments.Development);

        builder.ConfigureAppConfiguration((_, configuration) =>
        {
            configuration.AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["ConnectionStrings:Postgresql"] = _postgres.GetConnectionString(),
            });
        });

        builder.ConfigureServices(services =>
        {
            services.AddHttpClient("brreg")
                .ConfigurePrimaryHttpMessageHandler(() => BrregHandler);
        });
    }

    async Task IAsyncLifetime.DisposeAsync()
    {
        await base.DisposeAsync();
        await _postgres.DisposeAsync();
    }
}

[CollectionDefinition(Name)]
public sealed class CustomersApiCollection : ICollectionFixture<CustomersApiFactory>
{
    public const string Name = "CustomersApi";
}
