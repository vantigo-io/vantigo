using System.Globalization;
using System.Net;

using Microsoft.AspNetCore.DataProtection;
using Microsoft.AspNetCore.HttpOverrides;

using Network = System.Net.IPNetwork;

namespace Vantigo.Communications.Api;

internal static class InfrastructureConfiguration
{
    internal static void AddConfiguredDataProtection(IServiceCollection services, IConfiguration configuration, IHostEnvironment environment)
    {
        var dataProtection = services.AddDataProtection();
        var keysPath = configuration["DataProtection:KeysPath"];
        if (!string.IsNullOrWhiteSpace(keysPath))
        {
            var absolutePath = Path.IsPathRooted(keysPath) ? keysPath : Path.Combine(environment.ContentRootPath, keysPath);
            Directory.CreateDirectory(absolutePath);
            dataProtection.PersistKeysToFileSystem(new DirectoryInfo(absolutePath));
        }

        var applicationName = configuration["DataProtection:ApplicationName"];
        if (!string.IsNullOrWhiteSpace(applicationName)) dataProtection.SetApplicationName(applicationName);
    }

    internal static void ConfigureForwardedHeaders(IServiceCollection services, IConfiguration configuration)
    {
        services.Configure<ForwardedHeadersOptions>(options =>
        {
            options.ForwardedHeaders = ForwardedHeaders.XForwardedFor | ForwardedHeaders.XForwardedProto;
            options.ForwardLimit = 1;
            var proxies = Values(configuration, "ForwardedHeaders:KnownProxies").Select(ParseAddress).ToArray();
            var networks = Values(configuration, "ForwardedHeaders:KnownNetworks").Select(ParseNetwork).ToArray();
            if (proxies.Length > 0 || networks.Length > 0)
            {
                options.KnownProxies.Clear();
                options.KnownIPNetworks.Clear();
                foreach (var proxy in proxies) options.KnownProxies.Add(proxy);
                foreach (var network in networks) options.KnownIPNetworks.Add(network);
            }
        });
    }

    private static IPAddress ParseAddress((string Value, string Path) item) =>
        IPAddress.TryParse(item.Value, out var address)
            ? address
            : throw new InvalidOperationException($"Invalid forwarded-header configuration at '{item.Path}': '{item.Value}' is not an IP address.");

    private static Network ParseNetwork((string Value, string Path) item)
    {
        var slash = item.Value.LastIndexOf('/');
        if (slash <= 0 || !IPAddress.TryParse(item.Value[..slash].Trim(), out var address) ||
            !int.TryParse(item.Value[(slash + 1)..].Trim(), NumberStyles.None, CultureInfo.InvariantCulture, out var length) ||
            length < 0 || length > (address.AddressFamily == System.Net.Sockets.AddressFamily.InterNetwork ? 32 : 128))
            throw new InvalidOperationException($"Invalid forwarded-header network configuration at '{item.Path}': '{item.Value}'.");
        return new Network(address, length);
    }

    private static IEnumerable<(string Value, string Path)> Values(IConfiguration configuration, string key)
    {
        var section = configuration.GetSection(key);
        if (!string.IsNullOrWhiteSpace(section.Value))
            return section.Value.Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries)
                .Select((value, index) => (value, $"{key}[{index}]"));
        return section.GetChildren().Where(child => !string.IsNullOrWhiteSpace(child.Value))
            .Select(child => (child.Value!.Trim(), child.Path));
    }
}
