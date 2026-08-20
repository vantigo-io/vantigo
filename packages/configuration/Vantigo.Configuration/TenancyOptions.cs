using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

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

    /// <summary>
    /// Escape hatch that allows <see cref="MultiMode"/> to start outside the
    /// Development environment. Multi-tenant authorization is not tenant-scoped
    /// yet (https://github.com/vantigo-io/vantigo/issues/7), so this must stay
    /// false in every real deployment.
    /// </summary>
    public bool AllowUnsafeMultiTenant { get; set; }

    public bool IsMultiTenant => string.Equals(Mode, MultiMode, StringComparison.Ordinal);

    public void Validate(bool isDevelopment)
    {
        Mode = StorageOptions.Normalize(Mode) ?? SingleMode;

        if (Mode is not (SingleMode or MultiMode))
            throw new InvalidOperationException(
                $"Tenancy configuration error: Mode must be {SingleMode} or {MultiMode}.");

        if (IsMultiTenant && !isDevelopment && !AllowUnsafeMultiTenant)
            throw new InvalidOperationException(
                "Tenancy configuration error: Mode=multi is not production-ready and refuses to start outside " +
                "Development. Multi-tenant authorization is installation-global: roles, role catalogs and Owner " +
                "user management are not scoped to a tenant, so an Owner of one tenant can enumerate and modify " +
                "users of every other tenant (https://github.com/vantigo-io/vantigo/issues/7). Use Mode=single, " +
                "or set Tenancy:AllowUnsafeMultiTenant=true to accept that cross-tenant exposure knowingly.");
    }
}

/// <summary>
/// Validates <see cref="TenancyOptions"/> against the hosting environment so the
/// unsafe multi-tenant mode fails closed at startup.
/// </summary>
internal sealed class TenancyOptionsValidator(IHostEnvironment environment) : IValidateOptions<TenancyOptions>
{
    public ValidateOptionsResult Validate(string? name, TenancyOptions options)
    {
        try
        {
            options.Validate(environment.IsDevelopment());
            return ValidateOptionsResult.Success;
        }
        catch (InvalidOperationException exception)
        {
            return ValidateOptionsResult.Fail(exception.Message);
        }
    }
}

public static class TenancyConfigurationExtensions
{
    public static IServiceCollection AddTenancyOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.AddOptions<TenancyOptions>()
            .Bind(configuration.GetSection(TenancyOptions.ConfigurationSectionName))
            .ValidateOnStart();
        services.AddSingleton<IValidateOptions<TenancyOptions>, TenancyOptionsValidator>();
        return services;
    }
}