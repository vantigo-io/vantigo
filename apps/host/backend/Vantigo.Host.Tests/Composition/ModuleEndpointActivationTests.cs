using System.Net;

using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;

using Testcontainers.PostgreSql;

using Vantigo.Host;
using Vantigo.Testing;

namespace Vantigo.Host.Tests.Composition;

/// <summary>
/// Boots the full host API against a real PostgreSQL instance with one module
/// switched off at a time. A disabled module has to be unreachable from outside
/// the process, not merely unmigrated: its routes must fall through to the
/// host's <c>/api</c> catch-all while every other module stays mapped.
/// </summary>
[Collection(ModuleEndpointActivationCollection.Name)]
public sealed class ModuleEndpointActivationTests(ModuleEndpointActivationDatabase database)
{
    public static TheoryData<string> Modules() =>
    [
        ModuleActivation.CustomersModule,
        ModuleActivation.CommunicationsModule,
        ModuleActivation.ProductsModule,
        ModuleActivation.EnergyModule,
    ];

    [Theory]
    [MemberData(nameof(Modules))]
    public async Task A_disabled_module_is_neither_reachable_nor_running_workers(string module)
    {
        ModuleActivation activation = Without(module);
        await using ModuleActivationApiFactory factory = new(
            database.ConnectionString, database.MigrationsConnectionString, activation);
        using HttpClient client = factory.CreateClient();

        HttpResponseMessage disabled = await client.GetAsync(RouteOf(module));

        Assert.Equal(HttpStatusCode.NotFound, disabled.StatusCode);

        // The other modules in the same host stay mapped, so the 404 above is the
        // module being absent rather than the whole API pipeline being broken.
        foreach (string other in activation.EnabledModuleNames)
        {
            HttpResponseMessage enabled = await client.GetAsync(RouteOf(other));
            Assert.NotEqual(HttpStatusCode.NotFound, enabled.StatusCode);
        }

        string[] workers = factory.Services.GetServices<IHostedService>()
            .Select(worker => worker.GetType().FullName ?? string.Empty)
            .Where(name => name.StartsWith($"Vantigo.{module}.", StringComparison.Ordinal))
            .ToArray();

        Assert.Empty(workers);
    }

    [Fact]
    public async Task Disabling_customers_while_communications_needs_it_fails_startup()
    {
        await using ModuleActivationApiFactory factory = new(
            database.ConnectionString,
            database.MigrationsConnectionString,
            new ModuleActivation(customers: false, communications: true, products: false, energy: false));

        Exception exception = Assert.ThrowsAny<Exception>(() => factory.CreateClient());

        Assert.Contains("Modules:Customers:Enabled=true", exception.ToString(), StringComparison.Ordinal);
    }

    /// <summary>
    /// Every module except <paramref name="module"/>, minus the modules that
    /// cannot be hosted without Customers.
    /// </summary>
    private static ModuleActivation Without(string module)
    {
        bool customers = module != ModuleActivation.CustomersModule;
        return new ModuleActivation(
            customers,
            communications: customers && module != ModuleActivation.CommunicationsModule,
            products: module != ModuleActivation.ProductsModule,
            energy: customers && module != ModuleActivation.EnergyModule);
    }

    private static string RouteOf(string module) => module switch
    {
        ModuleActivation.CustomersModule => "/api/v1/customers/stats",
        ModuleActivation.CommunicationsModule => "/api/v1/communications/conversations",
        ModuleActivation.ProductsModule => "/api/v1/products/1",
        ModuleActivation.EnergyModule => "/api/v1/energy/metering-points",
        _ => throw new ArgumentOutOfRangeException(nameof(module), module, "Unknown module name."),
    };
}

internal sealed class ModuleActivationApiFactory(
    string connectionString,
    string migrationsConnectionString,
    ModuleActivation activation)
    : WebApplicationFactory<global::Program>
{
    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        builder.UseEnvironment(Environments.Development);
        // Host configuration, so the flags are in place before the host composes
        // its modules and not only before options are first resolved.
        builder.UseSetting("Modules:Customers:Enabled", activation.Customers.ToString());
        builder.UseSetting("Modules:Communications:Enabled", activation.Communications.ToString());
        builder.UseSetting("Modules:Products:Enabled", activation.Products.ToString());
        builder.UseSetting("Modules:Energy:Enabled", activation.Energy.ToString());
        builder.ConfigureAppConfiguration((_, configuration) => configuration.AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["ConnectionStrings:vantigo"] = connectionString,
            ["ConnectionStrings:migrations"] = migrationsConnectionString,
            ["Development:Seed:Enabled"] = "false",
            ["Authentication:Bootstrap:Secret"] = "module-activation-bootstrap-secret",
            ["Authentication:PasswordReset:ResetUrl"] = "http://test.local/reset?email={email}&token={token}",
            ["Authentication:Invitations:AcceptUrl"] = "http://test.local/invitations?token={token}",
            // The workers must not poll a shared test database while the
            // assertions run; the point here is only whether they exist.
            ["Outbox:PollSeconds"] = "3600",
            ["Communications:Inbound:PollSeconds"] = "3600",
        }));
        builder.ConfigureServices(services =>
        {
            services.AddContractRecording();
            services.AddSingleton(new HostTestStartupPreparation(ApplyMigrations: true, SeedDevelopmentData: false));
        });
    }
}

/// <summary>
/// One PostgreSQL instance shared by every module combination in this
/// collection. Each host boot migrates only the modules it enables, and the
/// schemas of the others are simply absent.
/// </summary>
public sealed class ModuleEndpointActivationDatabase : IAsyncLifetime
{
    private readonly PostgreSqlContainer _postgres = new PostgreSqlBuilder("postgres:17-alpine")
        .WithDatabase("vantigo")
        .Build();

    public string ConnectionString { get; private set; } = string.Empty;

    public string MigrationsConnectionString => _postgres.GetConnectionString();

    public async Task InitializeAsync()
    {
        await _postgres.StartAsync();
        ConnectionString = await Vantigo.Tenancy.EntityFramework.TenantRuntimeRoleSql
            .ProvisionAsync(_postgres.GetConnectionString());
    }

    public async Task DisposeAsync() => await _postgres.DisposeAsync();
}

[CollectionDefinition(Name, DisableParallelization = true)]
public sealed class ModuleEndpointActivationCollection : ICollectionFixture<ModuleEndpointActivationDatabase>
{
    public const string Name = "ModuleEndpointActivation";
}