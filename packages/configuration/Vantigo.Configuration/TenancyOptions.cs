using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Configuration;

/// <summary>
/// How the application hosts tenants. Defaults to single-tenant, which requires
/// no configuration and auto-provisions a default tenant at startup.
/// </summary>
public sealed class TenancyOptions
{
    public const string ConfigurationSectionName = "Tenancy";

    public const string SingleMode = "single";
    public const string MultiMode = "multi";

    /// <summary>"single" (default) or "multi".</summary>
    public string? Mode { get; set; }

    public bool IsMultiTenant => string.Equals(Mode, MultiMode, StringComparison.Ordinal);

    public void Validate()
    {
        Mode = StorageOptions.Normalize(Mode) ?? SingleMode;

        if (Mode is not (SingleMode or MultiMode))
            throw new InvalidOperationException(
                $"Tenancy configuration error: Mode must be {SingleMode} or {MultiMode}.");
    }
}

public static class TenancyConfigurationExtensions
{
    public static IServiceCollection AddTenancyOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.AddOptions<TenancyOptions>()
            .Bind(configuration.GetSection(TenancyOptions.ConfigurationSectionName))
            .Validate(options =>
            {
                options.Validate();
                return true;
            })
            .ValidateOnStart();
        return services;
    }
}