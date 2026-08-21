using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Configuration;

/// <summary>
/// Per-module runtime enablement flags used by the host to decide which modules
/// to register, migrate, and seed.
/// </summary>
public sealed class ModuleHostingOptions
{
    /// <summary>The configuration section the per-module flags are bound from.</summary>
    public const string SectionName = "Modules";

    public ModuleToggleOptions Customers { get; set; } = new();

    public ModuleToggleOptions Communications { get; set; } = new();

    public ModuleToggleOptions Products { get; set; } = new();

    public ModuleToggleOptions Energy { get; set; } = new();
}

public sealed class ModuleToggleOptions
{
    public bool Enabled { get; set; } = true;
}

public static class ModuleHostingConfigurationExtensions
{
    /// <summary>
    /// Registers <see cref="ModuleHostingOptions"/> from the <c>Modules</c>
    /// configuration section.
    /// </summary>
    public static IServiceCollection AddModuleHostingOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.Configure<ModuleHostingOptions>(configuration.GetSection(ModuleHostingOptions.SectionName));
        return services;
    }
}