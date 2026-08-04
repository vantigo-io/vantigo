using System.Security.Cryptography;
using System.Text;

namespace Vantigo.Communications.Api.Services;

public sealed class CustomersApiKeyValidator(IConfiguration configuration)
{
    public bool IsValid(string? suppliedKey)
    {
        // The Enabled flag is an explicit integration gate. A configured key is
        // inert while the remote Customers integration is disabled.
        if (!configuration.GetValue<bool>("Customers:Enabled")) return false;

        var configured = configuration["Customers:ApiKey"];
        if (string.IsNullOrEmpty(configured) || string.IsNullOrEmpty(suppliedKey)) return false;
        var configuredBytes = Encoding.UTF8.GetBytes(configured);
        var suppliedBytes = Encoding.UTF8.GetBytes(suppliedKey);
        return configuredBytes.Length == suppliedBytes.Length && CryptographicOperations.FixedTimeEquals(configuredBytes, suppliedBytes);
    }
}
