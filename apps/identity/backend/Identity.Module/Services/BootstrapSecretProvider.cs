using System.Security.Cryptography;

using Microsoft.AspNetCore.WebUtilities;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;

namespace Vantigo.Identity.Services;

/// <summary>
/// Resolves the one-time bootstrap secret once for the process lifetime. A
/// configured value is used exactly and never logged. Outside Development an
/// absent value fails startup: it is never generated or logged there, because
/// every replica would otherwise mint a different secret and log it. In
/// Development only, an absent value is generated in memory, logged once for
/// the operator, and intentionally not persisted.
/// </summary>
public sealed class BootstrapSecretProvider
{
    public BootstrapSecretProvider(
        IOptions<VantigoAuthenticationOptions> options,
        IHostEnvironment environment,
        ILogger<BootstrapSecretProvider> logger)
    {
        var configuredSecret = options.Value.Bootstrap.Secret;
        if (!string.IsNullOrWhiteSpace(configuredSecret))
        {
            Secret = configuredSecret;
            return;
        }

        if (!environment.IsDevelopment())
        {
            throw new OptionsValidationException(
                Options.DefaultName,
                typeof(VantigoAuthenticationOptions),
                ["Authentication:Bootstrap:Secret is required outside Development. Configure an explicit high-entropy secret (for example, via Key Vault) before starting; it is never generated or logged outside Development."]);
        }

        Secret = WebEncoders.Base64UrlEncode(RandomNumberGenerator.GetBytes(32));
        logger.LogWarning(
            "No Authentication:Bootstrap:Secret is configured. Generated bootstrap secret {BootstrapSecret} is valid only until setup completes or this process restarts; treat it as a secret.",
            Secret);
    }

    public string Secret { get; }
}