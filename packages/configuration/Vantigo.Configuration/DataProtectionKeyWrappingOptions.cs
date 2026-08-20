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
    /// When unset, keys are persisted unwrapped; outside Development this
    /// fails startup, see the validator registered by
    /// <see cref="DataProtectionKeyWrappingConfigurationExtensions.AddDataProtectionOptions"/>.
    /// </summary>
    public string? KeyVaultKeyUri { get; set; }
}

public static class DataProtectionKeyWrappingConfigurationExtensions
{
    /// <summary>
    /// Registers <see cref="DataProtectionKeyWrappingOptions"/> from the
    /// <c>DataProtection</c> configuration section, with startup validation
    /// that requires key wrapping outside Development.
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

/// <summary>
/// Rejects an unwrapped Data Protection key ring outside Development. Without
/// key wrapping, a PostgreSQL dump contains both encrypted secrets (for
/// example mailbox credentials protected by <c>EmailSender</c>) and the keys
/// to decrypt them, and may enable auth-cookie/token forgery. There is no
/// opt-out: like <c>EmailOptionsValidator</c> and
/// <c>BootstrapSecretOptionsValidator</c>, this class of security-critical
/// misconfiguration fails closed outside Development with no bypass.
/// </summary>
internal sealed class DataProtectionKeyWrappingOptionsValidator(IHostEnvironment environment) : IValidateOptions<DataProtectionKeyWrappingOptions>
{
    public ValidateOptionsResult Validate(string? name, DataProtectionKeyWrappingOptions options)
    {
        if (environment.IsDevelopment() || !string.IsNullOrWhiteSpace(options.KeyVaultKeyUri))
            return ValidateOptionsResult.Success;

        return ValidateOptionsResult.Fail(
            "Data protection configuration error: DataProtection:KeyVaultKeyUri is required outside " +
            "Development. Configure an Azure Key Vault key identifier to wrap the PostgreSQL-persisted " +
            "key ring; without it, a database dump exposes both protected secrets and the keys to " +
            "decrypt them.");
    }
}