using System.Security.Cryptography;

using Microsoft.AspNetCore.WebUtilities;

namespace Vantigo.Products.Api.Services;

/// <summary>
/// Resolves the one-time bootstrap secret once for the process lifetime. A
/// configured value is used exactly; an absent value is generated in memory and
/// is intentionally not persisted.
/// </summary>
public sealed class BootstrapSecretProvider
{
    public BootstrapSecretProvider(IConfiguration configuration, ILogger<BootstrapSecretProvider> logger)
    {
        var configuredSecret = configuration["Authentication:Bootstrap:Secret"];
        if (!string.IsNullOrWhiteSpace(configuredSecret))
        {
            Secret = configuredSecret;
            return;
        }

        Secret = WebEncoders.Base64UrlEncode(RandomNumberGenerator.GetBytes(32));
        logger.LogWarning(
            "No Authentication:Bootstrap:Secret is configured. Generated bootstrap secret {BootstrapSecret} is valid only until setup completes or this process restarts; treat it as a secret.",
            Secret);
    }

    public string Secret { get; }
}