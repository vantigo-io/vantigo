using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

namespace Vantigo.Configuration;

/// <summary>
/// Data protection key-wrapping options. Configuring <see cref="KeyVaultKeyUri"/>
/// wraps the PostgreSQL-persisted Data Protection key ring with an Azure Key
/// Vault key, so a database dump alone can no longer decrypt protected
/// payloads (mailbox credentials, antiforgery tokens, protected Identity
/// tokens). Named distinctly from ASP.NET Core's own
/// <c>Microsoft.AspNetCore.DataProtection.DataProtectionOptions</c> to avoid a
/// naming collision.
/// </summary>
public sealed class DataProtectionKeyWrappingOptions
{
    /// <summary>
    /// The Azure Key Vault key identifier used to wrap the Data Protection key
    /// ring, for example
    /// "https://my-vault.vault.azure.net/keys/dataprotection/&lt;version&gt;".
    /// </summary>
    public string? KeyVaultKeyUri { get; set; }

    /// <summary>
    /// Escape hatch that allows an unwrapped (PostgreSQL-only) key ring outside
    /// Development. Vantigo does not yet provision Azure infrastructure, so no
    /// Key Vault exists for most deployments today
    /// (https://github.com/vantigo-io/vantigo/issues/8); this flag lets those
    /// deployments start deliberately, not by accident. A database dump then
    /// contains both the encrypted payloads and the keys that decrypt them.
    /// Unset this the moment a Key Vault key is available.
    /// </summary>
    public bool AllowUnwrappedKeys { get; set; }

    public void Validate(bool isDevelopment)
    {
        if (isDevelopment || !string.IsNullOrWhiteSpace(KeyVaultKeyUri) || AllowUnwrappedKeys)
            return;

        throw new InvalidOperationException(
            "Data protection configuration error: an unwrapped key ring is not production-ready and refuses " +
            "to start outside Development. Without Key Vault wrapping, a database dump contains both " +
            "encrypted payloads (for example mailbox credentials protected by EmailSender) and the keys to " +
            "decrypt them, and may enable auth-cookie/token forgery. Configure " +
            "DataProtection:KeyVaultKeyUri, or set DataProtection:AllowUnwrappedKeys=true to accept that " +
            "exposure knowingly (for example, before Azure infrastructure exists: " +
            "https://github.com/vantigo-io/vantigo/issues/8).");
    }
}

/// <summary>
/// Validates <see cref="DataProtectionKeyWrappingOptions"/> against the hosting
/// environment so an unwrapped key ring fails closed at startup unless
/// explicitly and knowingly accepted.
/// </summary>
internal sealed class DataProtectionKeyWrappingOptionsValidator(IHostEnvironment environment) : IValidateOptions<DataProtectionKeyWrappingOptions>
{
    public ValidateOptionsResult Validate(string? name, DataProtectionKeyWrappingOptions options)
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

public static class DataProtectionKeyWrappingConfigurationExtensions
{
    /// <summary>
    /// Registers <see cref="DataProtectionKeyWrappingOptions"/> from the
    /// <c>DataProtection</c> configuration section, with startup validation
    /// that requires key wrapping (or the explicit escape hatch) outside
    /// Development.
    /// </summary>
    public static IServiceCollection AddDataProtectionOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.AddOptions<DataProtectionKeyWrappingOptions>()
            .Bind(configuration.GetSection("DataProtection"))
            .ValidateOnStart();
        services.AddSingleton<IValidateOptions<DataProtectionKeyWrappingOptions>, DataProtectionKeyWrappingOptionsValidator>();
        return services;
    }
}