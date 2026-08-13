using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Configuration;

/// <summary>
/// Registers the shared Vantigo configuration options and services.
/// </summary>
public static class VantigoConfigurationServiceCollectionExtensions
{
    /// <summary>
    /// Adds all Vantigo configuration options and the <see cref="AppPublicUrls"/>
    /// service.
    /// </summary>
    public static IServiceCollection AddVantigoConfiguration(this IServiceCollection services, IConfiguration configuration)
    {
        services.AddConnectionStringsOptions(configuration);
        services.AddAppBasePathOptions(configuration);
        services.AddAppPublicOriginOptions(configuration);
        services.AddAppBrandingOptions(configuration);
        services.AddDevelopmentSeedOptions(configuration);
        services.AddBrregLookupOptions(configuration);
        services.AddCommunicationsOptions(configuration);
        services.AddSmtpOptions(configuration);
        services.AddVantigoAuthenticationOptions(configuration);
        services.AddEmailOptions(configuration);
        services.AddDataProtectionPostgreSqlOptions(configuration);
        services.AddVantigoForwardedHeaders();
        services.AddObservabilityOptions(configuration);
        services.AddModuleHostingOptions(configuration);
        services.AddWorkforceOidcOptions();
        services.AddScimOptions();
        services.AddSingleton<AppPublicUrls>();
        return services;
    }
}