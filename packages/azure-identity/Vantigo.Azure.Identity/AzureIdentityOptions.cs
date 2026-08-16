using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;

namespace Vantigo.Azure.Identity;

/// <summary>
/// Controls whether Vantigo exposes its process-wide Azure token credential.
/// </summary>
public sealed class AzureIdentityOptions
{
    public const string ConfigurationSectionName = "Azure:Identity";

    /// <summary>Azure identity is enabled unless explicitly disabled.</summary>
    public bool Enabled { get; set; } = true;
}

public static class AzureIdentityServiceCollectionExtensions
{
    public static IServiceCollection AddVantigoAzureIdentity(
        this IServiceCollection services,
        IConfiguration configuration)
    {
        ArgumentNullException.ThrowIfNull(services);
        ArgumentNullException.ThrowIfNull(configuration);

        var section = configuration.GetSection(AzureIdentityOptions.ConfigurationSectionName);
        services.AddOptions<AzureIdentityOptions>()
            .Configure(options => AzureIdentityOptionsBinder.Bind(configuration, section, options))
            .ValidateOnStart();
        services.AddSingleton<IValidateOptions<AzureIdentityOptions>>(_ =>
            new AzureIdentityOptionsValidator(configuration, section));

        // The decision to add the descriptor is made without constructing a credential.
        // DefaultAzureCredential itself is lazy and does not acquire a token here.
        if (AzureIdentityOptionsBinder.ReadEnabled(configuration, section) &&
            !services.Any(descriptor => descriptor.ServiceType == typeof(global::Azure.Core.TokenCredential)))
            services.AddSingleton<global::Azure.Core.TokenCredential, global::Azure.Identity.DefaultAzureCredential>();

        return services;
    }

    private static class AzureIdentityOptionsBinder
    {
        public static bool ReadEnabled(IConfiguration configuration, IConfigurationSection section)
        {
            var value = Read(configuration, section);
            return value is null || (bool.TryParse(value, out var enabled) && enabled);
        }

        public static void Bind(
            IConfiguration configuration,
            IConfigurationSection section,
            AzureIdentityOptions options)
        {
            var value = Read(configuration, section);
            if (value is not null && bool.TryParse(value, out var enabled))
                options.Enabled = enabled;
        }

        private static string? Read(IConfiguration configuration, IConfigurationSection section) =>
            section["Enabled"] ?? configuration["AZURE__IDENTITY__ENABLED"];
    }

    private sealed class AzureIdentityOptionsValidator(
        IConfiguration configuration,
        IConfigurationSection section) : IValidateOptions<AzureIdentityOptions>
    {
        public ValidateOptionsResult Validate(string? name, AzureIdentityOptions options)
        {
            var value = section["Enabled"] ?? configuration["AZURE__IDENTITY__ENABLED"];
            if (value is not null && !bool.TryParse(value, out _))
                return ValidateOptionsResult.Fail(
                    "Azure identity configuration error: Azure:Identity:Enabled must be true or false.");

            foreach (var child in section.GetChildren())
            {
                if (!string.Equals(child.Key, "Enabled", StringComparison.OrdinalIgnoreCase) || child.GetChildren().Any())
                    return ValidateOptionsResult.Fail(
                        $"Azure identity configuration error: unknown or malformed configuration key '{child.Path}'.");
            }

            return ValidateOptionsResult.Success;
        }
    }
}