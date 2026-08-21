using Microsoft.AspNetCore.Builder;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;

using Vantigo.Configuration;
using Vantigo.Contracts;
using Vantigo.Contracts.AspNetCore.Authorization;
using Vantigo.Contracts.Authorization;
using Vantigo.Host;

namespace Vantigo.Host.Tests.Composition;

/// <summary>
/// Composes the real host for every combination of <c>Modules:*:Enabled</c>.
/// A disabled module must contribute nothing at all — no services, no workers,
/// no permissions, no endpoints — and every hostable combination must still
/// build and pass the permission catalog validation the API pipeline runs right
/// after the module endpoints are mapped. That last assertion is the one that
/// catches a registration decision drifting apart from the mapping decision.
/// </summary>
public sealed class ModuleActivationTests
{
    private static readonly bool[] Flags = [false, true];

    public static IEnumerable<object[]> HostableCombinations() =>
        from customers in Flags
        from communications in Flags
        from products in Flags
        from energy in Flags
        where customers || (!communications && !energy)
        select new object[] { customers, communications, products, energy };

    public static IEnumerable<object[]> UnhostableCombinations() =>
        from communications in Flags
        from products in Flags
        from energy in Flags
        where communications || energy
        select new object[] { communications, products, energy };

    [Theory]
    [MemberData(nameof(HostableCombinations))]
    public void Every_hostable_combination_builds_maps_and_validates(
        bool customers, bool communications, bool products, bool energy)
    {
        ModuleActivation expected = new(customers, communications, products, energy);

        using WebApplication app = Build(expected);

        ModuleActivation activation = app.Services.GetRequiredService<ModuleActivation>();
        Assert.Equal(customers, activation.Customers);
        Assert.Equal(communications, activation.Communications);
        Assert.Equal(products, activation.Products);
        Assert.Equal(energy, activation.Energy);

        // The pipeline order the API command uses: map the modules, then assert
        // every mapped endpoint's permission is in the composed catalog.
        Program.MapEnabledModules(app);
        app.ValidatePermissionCatalog(app.Services.GetRequiredService<IPermissionCatalog>());
    }

    [Theory]
    [MemberData(nameof(HostableCombinations))]
    public void A_disabled_module_contributes_no_permissions(
        bool customers, bool communications, bool products, bool energy)
    {
        using WebApplication app = Build(new ModuleActivation(customers, communications, products, energy));

        IPermissionCatalog catalog = app.Services.GetRequiredService<IPermissionCatalog>();

        Assert.Equal(customers, catalog.Permissions.Any(permission => permission.Module == "customers"));
        Assert.Equal(communications, catalog.Permissions.Any(permission => permission.Module == "communications"));
        Assert.Equal(products, catalog.Permissions.Any(permission => permission.Module == "products"));
        Assert.Equal(energy, catalog.Permissions.Any(permission => permission.Module == "energy"));
        // Identity is not a module flag; its permissions survive every combination.
        Assert.True(catalog.Contains("identity:manage"));
    }

    [Theory]
    [InlineData(true)]
    [InlineData(false)]
    public void The_communications_workers_follow_the_communications_flag(bool communications)
    {
        WebApplicationBuilder builder = CreateBuilder(
            new ModuleActivation(customers: true, communications, products: false, energy: false));

        string[] workers = builder.Services
            .Where(descriptor => descriptor.ServiceType == typeof(IHostedService))
            .Select(descriptor => descriptor.ImplementationType?.FullName ?? string.Empty)
            .Where(name => name.StartsWith("Vantigo.Communications.", StringComparison.Ordinal))
            .Order(StringComparer.Ordinal)
            .ToArray();

        string[] expected = communications
            ?
            [
                "Vantigo.Communications.Services.CommunicationsAttachmentCleanupWorker",
                "Vantigo.Communications.Services.CommunicationsAttachmentScannerWorker",
                "Vantigo.Communications.Services.CommunicationsInboundWorker",
                "Vantigo.Communications.Services.CommunicationsOutboxWorker",
                "Vantigo.Communications.Services.CommunicationsRetentionWorker",
            ]
            : [];

        Assert.Equal(expected, workers);
    }

    [Theory]
    [InlineData(true)]
    [InlineData(false)]
    public void A_disabled_module_registers_none_of_its_services(bool customers)
    {
        WebApplicationBuilder builder = CreateBuilder(
            new ModuleActivation(customers, communications: false, products: false, energy: false));

        Assert.Equal(
            customers,
            builder.Services.Any(descriptor => descriptor.ServiceType == typeof(ICustomerDirectory)));
    }

    [Theory]
    [MemberData(nameof(UnhostableCombinations))]
    public void Communications_or_energy_without_customers_is_rejected(bool communications, bool products, bool energy)
    {
        InvalidOperationException exception = Assert.Throws<InvalidOperationException>(() =>
            CreateBuilder(new ModuleActivation(customers: false, communications, products, energy)));

        Assert.Contains("Modules:Customers:Enabled=true", exception.Message, StringComparison.Ordinal);
        Assert.Contains(nameof(ICustomerDirectory), exception.Message, StringComparison.Ordinal);
        if (communications)
            Assert.Contains("the Communications module requires", exception.Message, StringComparison.Ordinal);
        if (energy)
            Assert.Contains("the Energy module requires", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void The_activation_binds_every_flag_from_the_modules_section()
    {
        ConfigurationManager configuration = new();
        configuration.AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["Modules:Customers:Enabled"] = "true",
            ["Modules:Communications:Enabled"] = "false",
            ["Modules:Products:Enabled"] = "true",
            ["Modules:Energy:Enabled"] = "false",
        });

        ModuleActivation activation = ModuleActivation.FromConfiguration(configuration);

        Assert.True(activation.Customers);
        Assert.False(activation.Communications);
        Assert.True(activation.Products);
        Assert.False(activation.Energy);
        Assert.Equal(["Customers", "Products"], activation.EnabledModuleNames);
    }

    [Fact]
    public void An_absent_modules_section_leaves_every_module_enabled()
    {
        ConfigurationManager configuration = new();

        ModuleActivation activation = ModuleActivation.FromConfiguration(configuration);

        Assert.Equal(
            ["Customers", "Communications", "Energy", "Products"],
            activation.EnabledModuleNames);
    }

    [Fact]
    public void The_activation_matches_the_options_the_rest_of_the_installation_reads()
    {
        ModuleHostingOptions options = new();
        options.Communications.Enabled = false;
        options.Energy.Enabled = false;

        ModuleActivation activation = ModuleActivation.FromOptions(options);

        Assert.True(activation.IsEnabled(ModuleActivation.CustomersModule));
        Assert.False(activation.IsEnabled(ModuleActivation.CommunicationsModule));
        Assert.True(activation.IsEnabled(ModuleActivation.ProductsModule));
        Assert.False(activation.IsEnabled(ModuleActivation.EnergyModule));
    }

    [Fact]
    public void Reading_the_decision_back_rejects_a_configuration_that_has_since_changed()
    {
        ServiceCollection services = new();
        services.AddSingleton(new ModuleActivation(customers: true, communications: false, products: true, energy: true));
        services.AddOptions<ModuleHostingOptions>().Configure(options => options.Communications.Enabled = true);
        using ServiceProvider provider = services.BuildServiceProvider();

        InvalidOperationException exception = Assert.Throws<InvalidOperationException>(
            () => ModuleActivation.Resolve(provider));

        Assert.Contains("Customers, Energy, Products", exception.Message, StringComparison.Ordinal);
        Assert.Contains("IWebHostBuilder.UseSetting", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void Reading_the_decision_back_returns_the_composed_one()
    {
        ModuleActivation composed = new(customers: true, communications: true, products: true, energy: true);
        ServiceCollection services = new();
        services.AddSingleton(composed);
        services.AddOptions<ModuleHostingOptions>();
        using ServiceProvider provider = services.BuildServiceProvider();

        Assert.Same(composed, ModuleActivation.Resolve(provider));
    }

    private static WebApplication Build(ModuleActivation activation) => CreateBuilder(activation).Build();

    private static WebApplicationBuilder CreateBuilder(ModuleActivation activation)
    {
        WebApplicationBuilder builder = WebApplication.CreateBuilder(new WebApplicationOptions
        {
            EnvironmentName = Environments.Development,
            ApplicationName = typeof(global::Program).Assembly.GetName().Name,
        });
        builder.Configuration.AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["ConnectionStrings:vantigo"] = "Host=localhost;Port=5432;Database=vantigo;Username=vantigo;Password=vantigo",
            ["Modules:Customers:Enabled"] = activation.Customers.ToString(),
            ["Modules:Communications:Enabled"] = activation.Communications.ToString(),
            ["Modules:Products:Enabled"] = activation.Products.ToString(),
            ["Modules:Energy:Enabled"] = activation.Energy.ToString(),
            ["Development:Seed:Enabled"] = "false",
        });
        builder.Host.UseDefaultServiceProvider((_, options) =>
        {
            options.ValidateOnBuild = true;
            options.ValidateScopes = true;
        });

        Program.RegisterHostServices(builder, VantigoCommand.Api);
        return builder;
    }
}