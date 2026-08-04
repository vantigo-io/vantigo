using System.Security.Cryptography;

using Microsoft.AspNetCore.WebUtilities;

namespace Vantigo.Communications.Api.Services;

public sealed class BootstrapSecretProvider
{
    public BootstrapSecretProvider(
        IConfiguration configuration,
        IHostEnvironment environment,
        ILogger<BootstrapSecretProvider> logger)
    {
        Secret = configuration["Authentication:Bootstrap:Secret"] ?? string.Empty;
        if (string.IsNullOrWhiteSpace(Secret))
        {
            if (!environment.IsDevelopment())
            {
                throw new InvalidOperationException(
                    "Authentication:Bootstrap:Secret must be configured outside Development. " +
                    "Use an environment variable, user secret, or deployment secret store; generated bootstrap secrets are not permitted.");
            }

            Secret = WebEncoders.Base64UrlEncode(RandomNumberGenerator.GetBytes(32));
            logger.LogWarning(
                "No Authentication:Bootstrap:Secret is configured. Generated local bootstrap secret {BootstrapSecret} is valid only for this process.",
                Secret);
        }
    }

    public string Secret { get; }
}