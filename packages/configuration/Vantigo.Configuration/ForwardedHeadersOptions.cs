using System.Globalization;
using System.Net;

using Microsoft.AspNetCore.HttpOverrides;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;

using Network = System.Net.IPNetwork;

namespace Vantigo.Configuration;

/// <summary>
/// Forwarded-headers middleware configuration. Provide known proxies and
/// networks as arrays of strings.
/// </summary>
public sealed class ForwardedHeadersConfigurationOptions
{
    /// <summary>
    /// Known proxy IP addresses.
    /// </summary>
    public string[]? KnownProxies { get; set; }

    /// <summary>
    /// Known networks in CIDR notation, e.g. <c>"10.0.0.0/8"</c>.
    /// </summary>
    public string[]? KnownNetworks { get; set; }

    /// <summary>
    /// Maximum number of entries to process from the forwarded headers.
    /// </summary>
    public int ForwardLimit { get; set; } = 1;
}

public static class ForwardedHeadersConfigurationExtensions
{
    /// <summary>
    /// Registers <see cref="ForwardedHeadersConfigurationOptions"/> from the
    /// <c>ForwardedHeaders</c> configuration section and applies the configured
    /// values to ASP.NET Core's <see cref="ForwardedHeadersOptions"/>.
    /// </summary>
    public static IServiceCollection AddVantigoForwardedHeaders(this IServiceCollection services)
    {
        services.AddOptions<ForwardedHeadersConfigurationOptions>();
        services.ConfigureOptions<VantigoForwardedHeadersConfigureOptions>();
        return services;
    }
}

internal sealed class VantigoForwardedHeadersConfigureOptions : IConfigureOptions<ForwardedHeadersOptions>
{
    private readonly IOptions<ForwardedHeadersConfigurationOptions> configurationOptions;

    public VantigoForwardedHeadersConfigureOptions(IOptions<ForwardedHeadersConfigurationOptions> configurationOptions)
    {
        this.configurationOptions = configurationOptions;
    }

    public void Configure(ForwardedHeadersOptions options)
    {
        var configured = configurationOptions.Value;

        options.ForwardedHeaders = ForwardedHeaders.XForwardedFor | ForwardedHeaders.XForwardedProto;
        options.ForwardLimit = configured.ForwardLimit is > 0 ? configured.ForwardLimit : 1;

        var proxies = (configured.KnownProxies ?? [])
            .Select((value, index) =>
                IPAddress.TryParse(value, out var address)
                    ? address
                    : throw new InvalidOperationException($"Invalid forwarded-header proxy configuration at 'ForwardedHeaders:KnownProxies[{index}]': '{value}'."))
            .ToArray();

        var networks = (configured.KnownNetworks ?? [])
            .Select(ParseNetwork)
            .ToArray();

        if (proxies.Length > 0 || networks.Length > 0)
        {
            options.KnownProxies.Clear();
            options.KnownIPNetworks.Clear();
            foreach (var proxy in proxies) options.KnownProxies.Add(proxy);
            foreach (var network in networks) options.KnownIPNetworks.Add(network);
        }
    }

    private static Network ParseNetwork(string value)
    {
        var separator = value.LastIndexOf('/');
        if (separator <= 0 || !IPAddress.TryParse(value[..separator].Trim(), out var address) ||
            !int.TryParse(value[(separator + 1)..].Trim(), NumberStyles.None, CultureInfo.InvariantCulture, out var length) ||
            length < 0 || length > (address.AddressFamily == System.Net.Sockets.AddressFamily.InterNetwork ? 32 : 128))
        {
            throw new InvalidOperationException($"Invalid forwarded-header network configuration: '{value}'.");
        }

        return new Network(address, length);
    }
}