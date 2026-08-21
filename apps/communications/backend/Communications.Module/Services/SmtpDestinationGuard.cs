using System.Net;
using System.Net.Sockets;

using Microsoft.Extensions.Options;

using Vantigo.Configuration;

namespace Vantigo.Communications.Services;

/// <summary>
/// Thrown when an SMTP destination is not permitted: it did not resolve, or it
/// resolved to a private, link-local, loopback, or reserved address and was
/// neither allowlisted nor covered by <see cref="CommunicationsSmtpOptions.AllowPrivateNetworks"/>.
/// </summary>
internal sealed class SmtpDestinationRejectedException(string message) : InvalidOperationException(message);

/// <summary>
/// Resolves a hostname to its addresses. Extracted from <see cref="SmtpDestinationGuard"/>
/// so tests can simulate a DNS rebinding response (a private address returned at
/// connect time) without a real network call.
/// </summary>
internal interface IDnsResolver
{
    Task<IPAddress[]> ResolveAsync(string host, CancellationToken cancellationToken);
}

internal sealed class SystemDnsResolver : IDnsResolver
{
    public async Task<IPAddress[]> ResolveAsync(string host, CancellationToken cancellationToken) =>
        await Dns.GetHostAddressesAsync(host, cancellationToken);
}

/// <summary>
/// Vets an SMTP destination host immediately before it is connected to, and
/// hands back the single <see cref="IPAddress"/> that was vetted. Callers must
/// connect the socket to that exact address - never re-resolve the hostname
/// afterwards - while still presenting the hostname for TLS SNI and certificate
/// validation. Resolving once (for example, when a channel is saved) and
/// connecting by hostname later would let a DNS answer change between the check
/// and the connect: a DNS rebinding attack that would otherwise defeat this
/// guard entirely.
/// </summary>
internal interface ISmtpDestinationGuard
{
    Task<IPAddress> VetAsync(string host, CancellationToken cancellationToken);
}

internal sealed class SmtpDestinationGuard(IDnsResolver resolver, IOptions<CommunicationsOptions> options) : ISmtpDestinationGuard
{
    public async Task<IPAddress> VetAsync(string host, CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(host)) throw new SmtpDestinationRejectedException("An SMTP host is required.");

        CommunicationsSmtpOptions settings = options.Value.Smtp;
        bool allowlisted = IsAllowlisted(host, settings.AllowedHosts);
        IPAddress[] addresses = IPAddress.TryParse(host, out IPAddress? literal) ? [literal] : await ResolveAsync(host, cancellationToken);
        if (addresses.Length == 0) throw new SmtpDestinationRejectedException($"SMTP host '{host}' did not resolve to any address.");

        if (!allowlisted && !settings.AllowPrivateNetworks)
        {
            IPAddress? blocked = Array.Find(addresses, IsDisallowedDestination);
            if (blocked is not null)
            {
                throw new SmtpDestinationRejectedException(
                    $"SMTP host '{host}' resolves to {blocked}, a private, link-local, loopback, or reserved " +
                    "address, which is not a permitted SMTP destination. Add the host to " +
                    "Communications:Smtp:AllowedHosts to approve it explicitly, or set " +
                    "Communications:Smtp:AllowPrivateNetworks=true to allow private-network SMTP delivery.");
            }
        }

        return addresses[0];
    }

    private async Task<IPAddress[]> ResolveAsync(string host, CancellationToken cancellationToken)
    {
        try
        {
            return await resolver.ResolveAsync(host, cancellationToken);
        }
        catch (SocketException exception)
        {
            throw new SmtpDestinationRejectedException($"SMTP host '{host}' could not be resolved: {exception.Message}");
        }
    }

    private static bool IsAllowlisted(string host, string[] allowedHosts) =>
        allowedHosts.Length > 0 && Array.Exists(allowedHosts, allowed => string.Equals(allowed.Trim(), host, StringComparison.OrdinalIgnoreCase));

    /// <summary>
    /// True when <paramref name="address"/> is a private (RFC1918), unique-local
    /// (RFC4193), link-local (RFC3927, including the 169.254.169.254 cloud
    /// metadata address), loopback, or otherwise non-routable/reserved address.
    /// </summary>
    internal static bool IsDisallowedDestination(IPAddress address)
    {
        IPAddress candidate = address.IsIPv4MappedToIPv6 ? address.MapToIPv4() : address;
        if (IPAddress.IsLoopback(candidate) || candidate.Equals(IPAddress.Any) || candidate.Equals(IPAddress.IPv6Any)) return true;

        return candidate.AddressFamily switch
        {
            AddressFamily.InterNetwork => IsDisallowedIPv4(candidate),
            AddressFamily.InterNetworkV6 => IsDisallowedIPv6(candidate),
            _ => true,
        };
    }

    private static bool IsDisallowedIPv4(IPAddress address)
    {
        byte[] bytes = address.GetAddressBytes();
        return bytes[0] == 0                                     // 0.0.0.0/8 - "this network"
            || bytes[0] == 10                                    // 10.0.0.0/8 - RFC1918
            || (bytes[0] == 100 && bytes[1] is >= 64 and <= 127)  // 100.64.0.0/10 - carrier-grade NAT
            || bytes[0] == 127                                   // 127.0.0.0/8 - loopback
            || (bytes[0] == 169 && bytes[1] == 254)               // 169.254.0.0/16 - RFC3927 link-local, includes the 169.254.169.254 cloud metadata address
            || (bytes[0] == 172 && bytes[1] is >= 16 and <= 31)   // 172.16.0.0/12 - RFC1918
            || (bytes[0] == 192 && bytes[1] == 0 && bytes[2] == 0)  // 192.0.0.0/24 - IETF protocol assignments
            || (bytes[0] == 192 && bytes[1] == 168)               // 192.168.0.0/16 - RFC1918
            || bytes[0] >= 240;                                   // 240.0.0.0/4 - reserved, plus 255.255.255.255
    }

    private static bool IsDisallowedIPv6(IPAddress address)
    {
        if (address.IsIPv6LinkLocal || address.IsIPv6SiteLocal || address.IsIPv6Multicast) return true;
        byte[] bytes = address.GetAddressBytes();
        return (bytes[0] & 0xFE) == 0xFC; // fc00::/7 - RFC4193 unique local addresses
    }
}