using Microsoft.Extensions.Options;

using Vantigo.Configuration;

namespace Vantigo.Host;

/// <summary>
/// The single startup decision about which modules this process hosts.
/// </summary>
/// <remarks>
/// The decision is taken once, from <c>Modules:&lt;Name&gt;:Enabled</c>, before any
/// module registers anything, and is then registered in the container so that
/// service registration, endpoint mapping, worker registration, migration, and
/// seeding all read the same immutable answer. Reading the flags a second time
/// is what makes the composition fragile: a module whose endpoints are mapped
/// without its permission catalog contributor having been registered fails
/// <c>ValidatePermissionCatalog</c>, and the host refuses to start.
/// </remarks>
public sealed class ModuleActivation
{
    public const string CustomersModule = "Customers";
    public const string CommunicationsModule = "Communications";
    public const string ProductsModule = "Products";
    public const string EnergyModule = "Energy";

    public ModuleActivation(bool customers, bool communications, bool products, bool energy)
    {
        Customers = customers;
        Communications = communications;
        Products = products;
        Energy = energy;
    }

    public bool Customers { get; }

    public bool Communications { get; }

    public bool Products { get; }

    public bool Energy { get; }

    /// <summary>Binds the decision from the <c>Modules</c> configuration section.</summary>
    public static ModuleActivation FromConfiguration(IConfiguration configuration)
    {
        ArgumentNullException.ThrowIfNull(configuration);
        ModuleHostingOptions options = new();
        configuration.GetSection(ModuleHostingOptions.SectionName).Bind(options);
        return FromOptions(options);
    }

    public static ModuleActivation FromOptions(ModuleHostingOptions options)
    {
        ArgumentNullException.ThrowIfNull(options);
        return new ModuleActivation(
            options.Customers.Enabled,
            options.Communications.Enabled,
            options.Products.Enabled,
            options.Energy.Enabled);
    }

    /// <summary>
    /// Reads the decision back out of the container, refusing to continue if the
    /// <c>Modules</c> configuration has since resolved to something else.
    /// </summary>
    /// <remarks>
    /// The flags are read while the service collection is still being populated,
    /// so a configuration source added after that point — notably
    /// <c>IWebHostBuilder.ConfigureAppConfiguration</c> in a test host, which is
    /// applied when the host is built — would migrate and map a different set of
    /// modules than the one the process actually composed. Test hosts supply the
    /// flags as host configuration (<c>IWebHostBuilder.UseSetting</c>) instead.
    /// </remarks>
    public static ModuleActivation Resolve(IServiceProvider services)
    {
        ArgumentNullException.ThrowIfNull(services);
        ModuleActivation composed = services.GetRequiredService<ModuleActivation>();
        ModuleHostingOptions? options = services.GetService<IOptions<ModuleHostingOptions>>()?.Value;
        if (options is null) return composed;

        ModuleActivation configured = FromOptions(options);
        if (configured.Matches(composed)) return composed;

        throw new InvalidOperationException(
            "The Modules configuration changed after the host composed its modules: it composed [" +
            string.Join(", ", composed.EnabledModuleNames) + "] but Modules:*:Enabled now resolves to [" +
            string.Join(", ", configured.EnabledModuleNames) + "]. The flags must be in place before the " +
            "host populates its service collection; supply them as host configuration " +
            "(IWebHostBuilder.UseSetting) rather than from a source applied while the host is built.");
    }

    public bool Matches(ModuleActivation other)
    {
        ArgumentNullException.ThrowIfNull(other);
        return Customers == other.Customers &&
            Communications == other.Communications &&
            Products == other.Products &&
            Energy == other.Energy;
    }

    public bool IsEnabled(string moduleName) => moduleName switch
    {
        CustomersModule => Customers,
        CommunicationsModule => Communications,
        ProductsModule => Products,
        EnergyModule => Energy,
        _ => throw new ArgumentOutOfRangeException(nameof(moduleName), moduleName, "Unknown module name."),
    };

    /// <summary>The enabled module names, ordered, for diagnostics and error messages.</summary>
    public IReadOnlyList<string> EnabledModuleNames
    {
        get
        {
            List<string> names = [];
            if (Customers) names.Add(CustomersModule);
            if (Communications) names.Add(CommunicationsModule);
            if (Energy) names.Add(EnergyModule);
            if (Products) names.Add(ProductsModule);
            return names;
        }
    }
}