using System.Globalization;
using System.Net;

using Microsoft.AspNetCore.DataProtection;
using Microsoft.AspNetCore.HttpOverrides;
using Microsoft.Extensions.Hosting;

using Network = System.Net.IPNetwork;

namespace Vantigo.Products.Api;

internal static class InfrastructureConfiguration
{
    internal static void AddConfiguredDataProtection(
        IServiceCollection services,
        IConfiguration configuration,
        IHostEnvironment environment)
    {
        var dataProtection = services.AddDataProtection();
        var keysPath = configuration["DataProtection:KeysPath"];
        if (!string.IsNullOrWhiteSpace(keysPath))
        {
            var absolutePath = Path.IsPathRooted(keysPath)
                ? keysPath
                : Path.Combine(environment.ContentRootPath, keysPath);
            Directory.CreateDirectory(absolutePath);
            dataProtection.PersistKeysToFileSystem(new DirectoryInfo(absolutePath));
        }

        var applicationName = configuration["DataProtection:ApplicationName"];
        if (!string.IsNullOrWhiteSpace(applicationName))
        {
            dataProtection.SetApplicationName(applicationName);
        }
    }

    internal static void ConfigureForwardedHeaders(IServiceCollection services, IConfiguration configuration)
    {
        services.Configure<ForwardedHeadersOptions>(options =>
        {
            options.ForwardedHeaders = ForwardedHeaders.XForwardedFor | ForwardedHeaders.XForwardedProto;
            options.ForwardLimit = 1;

            var configuredProxies = ParseConfiguredProxies(configuration);
            var configuredNetworks = ParseConfiguredNetworks(configuration);

            // Preserve ASP.NET Core's safe defaults when no explicit allowlist is
            // supplied. Replace them only after all configured values are validated.
            if (configuredProxies.Count > 0 || configuredNetworks.Count > 0)
            {
                options.KnownProxies.Clear();
                options.KnownIPNetworks.Clear();
                foreach (var proxy in configuredProxies)
                {
                    options.KnownProxies.Add(proxy);
                }

                foreach (var network in configuredNetworks)
                {
                    options.KnownIPNetworks.Add(network);
                }
            }
        });
    }

    private static List<IPAddress> ParseConfiguredProxies(IConfiguration configuration)
    {
        var addresses = new List<IPAddress>();
        foreach (var (value, path) in ConfigurationValues(configuration, "ForwardedHeaders:KnownProxies"))
        {
            if (!IPAddress.TryParse(value, out var address))
            {
                throw InvalidForwardedHeadersValue(path, value, "an IPv4 or IPv6 address");
            }

            addresses.Add(address);
        }

        return addresses;
    }

    private static List<Network> ParseConfiguredNetworks(IConfiguration configuration)
    {
        var section = configuration.GetSection("ForwardedHeaders:KnownNetworks");
        var networks = new List<Network>();

        if (!string.IsNullOrWhiteSpace(section.Value))
        {
            foreach (var (value, path) in ConfigurationValues(configuration, "ForwardedHeaders:KnownNetworks"))
            {
                networks.Add(ParseNetwork(value, path));
            }

            return networks;
        }

        // Supports KnownNetworks:0 = "10.0.0.0/8" and object entries with
        // Prefix/PrefixLength, including IPv4 and IPv6 networks.
        foreach (var child in section.GetChildren())
        {
            if (!string.IsNullOrWhiteSpace(child.Value))
            {
                networks.Add(ParseNetwork(child.Value.Trim(), child.Path));
            }
            else if (child.GetChildren().Any())
            {
                networks.Add(ParseNetworkObject(child, child.Path));
            }
        }

        if (section["Prefix"] is not null || section["PrefixLength"] is not null)
        {
            networks.Add(ParseNetworkObject(section, section.Path));
        }

        return networks;
    }

    private static Network ParseNetworkObject(IConfigurationSection section, string path)
    {
        var prefix = section["Prefix"]?.Trim();
        var prefixLength = section["PrefixLength"]?.Trim();
        if (string.IsNullOrWhiteSpace(prefix) || string.IsNullOrWhiteSpace(prefixLength))
        {
            throw new InvalidOperationException(
                $"Invalid forwarded-header network configuration at '{path}'. Both 'Prefix' and 'PrefixLength' are required.");
        }

        if (!IPAddress.TryParse(prefix, out var address) ||
            !int.TryParse(prefixLength, NumberStyles.None, CultureInfo.InvariantCulture, out var length) ||
            length < 0 || length > MaximumPrefixLength(address))
        {
            throw InvalidNetwork(path, $"{prefix}/{prefixLength}");
        }

        return new Network(address, length);
    }

    private static Network ParseNetwork(string value, string path)
    {
        var separator = value.LastIndexOf('/');
        if (separator <= 0 || separator == value.Length - 1)
        {
            throw InvalidNetwork(path, value);
        }

        var addressValue = value[..separator].Trim();
        var prefixValue = value[(separator + 1)..].Trim();
        if (!IPAddress.TryParse(addressValue, out var address) ||
            !int.TryParse(prefixValue, NumberStyles.None, CultureInfo.InvariantCulture, out var length) ||
            length < 0 || length > MaximumPrefixLength(address))
        {
            throw InvalidNetwork(path, value);
        }

        return new Network(address, length);
    }

    private static int MaximumPrefixLength(IPAddress address) =>
        address.AddressFamily == System.Net.Sockets.AddressFamily.InterNetwork ? 32 : 128;

    private static InvalidOperationException InvalidForwardedHeadersValue(string path, string value, string expected) =>
        new($"Invalid forwarded-header configuration at '{path}': '{value}' is not {expected}.");

    private static InvalidOperationException InvalidNetwork(string path, string value) =>
        new($"Invalid forwarded-header network configuration at '{path}': '{value}' must be a valid IPv4/IPv6 CIDR network.");

    private static IEnumerable<(string Value, string Path)> ConfigurationValues(IConfiguration configuration, string key)
    {
        var section = configuration.GetSection(key);
        if (!string.IsNullOrWhiteSpace(section.Value))
        {
            return section.Value
                .Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries)
                .Select((value, index) => (value, $"{key}[{index}]"));
        }

        return section.GetChildren()
            .Where(child => !string.IsNullOrWhiteSpace(child.Value))
            .Select(child => (child.Value!.Trim(), child.Path));
    }
}