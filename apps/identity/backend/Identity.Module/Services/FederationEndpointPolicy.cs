using System.Buffers;
using System.Net;
using System.Net.Sockets;

namespace Vantigo.Identity.Services;

/// <summary>Shared network and bounded-response policy for federation egress.</summary>
internal static class FederationEndpointPolicy
{
    internal const int MaxResponseBytes = 1024 * 1024;

    internal static bool TryValidateHttpsEndpoint(string? value, out string? endpoint)
    {
        endpoint = null;
        if (string.IsNullOrWhiteSpace(value) || !Uri.TryCreate(value.Trim(), UriKind.Absolute, out var uri) ||
            uri.Scheme != Uri.UriSchemeHttps || string.IsNullOrWhiteSpace(uri.Host) ||
            !string.IsNullOrEmpty(uri.UserInfo) || !string.IsNullOrEmpty(uri.Query) ||
            !string.IsNullOrEmpty(uri.Fragment)) return false;
        endpoint = value.Trim();
        return true;
    }

    internal static async Task<bool> IsPublicEndpointAsync(
        string endpoint, IFederationHostAddressResolver resolver, CancellationToken cancellationToken)
    {
        if (!TryValidateHttpsEndpoint(endpoint, out var safe) || !Uri.TryCreate(safe, UriKind.Absolute, out var uri)) return false;
        if (IPAddress.TryParse(uri.DnsSafeHost, out var literal)) return IsPublicAddress(literal);
        try
        {
            var addresses = await resolver.ResolveAsync(uri.DnsSafeHost, cancellationToken);
            return addresses.Length > 0 && addresses.All(IsPublicAddress);
        }
        catch (SocketException) { return false; }
        catch (HttpRequestException) { return false; }
    }

    internal static async Task<byte[]> ReadBoundedAsync(HttpContent content, CancellationToken cancellationToken)
    {
        if (content.Headers.ContentLength > MaxResponseBytes) throw new InvalidDataException();
        await using var stream = await content.ReadAsStreamAsync(cancellationToken);
        using var result = new MemoryStream();
        var buffer = ArrayPool<byte>.Shared.Rent(8192);
        try
        {
            var total = 0;
            int read;
            while ((read = await stream.ReadAsync(buffer.AsMemory(0, buffer.Length), cancellationToken)) > 0)
            {
                total += read;
                if (total > MaxResponseBytes) throw new InvalidDataException();
                result.Write(buffer, 0, read);
            }
            return result.ToArray();
        }
        finally { ArrayPool<byte>.Shared.Return(buffer); }
    }

    internal static bool IsPublicAddress(IPAddress address)
    {
        if (address.IsIPv4MappedToIPv6) address = address.MapToIPv4();
        if (IPAddress.IsLoopback(address) || address.Equals(IPAddress.Any) || address.Equals(IPAddress.IPv6Any) ||
            address.Equals(IPAddress.None) || address.Equals(IPAddress.IPv6None) || address.IsIPv6LinkLocal ||
            address.IsIPv6SiteLocal || address.IsIPv6Multicast || address.Equals(IPAddress.IPv6Loopback)) return false;
        var bytes = address.GetAddressBytes();
        if (address.AddressFamily == AddressFamily.InterNetwork)
        {
            var first = bytes[0]; var second = bytes[1];
            return first != 0 && first != 10 && first != 127 && first < 224 &&
                !(first == 100 && second is >= 64 and <= 127) && !(first == 169 && second == 254) &&
                !(first == 172 && second is >= 16 and <= 31) && !(first == 192 && second == 0) &&
                !(first == 192 && second == 168) && !(first == 198 && second is >= 18 and <= 19) &&
                !(first == 198 && second == 51 && bytes[2] == 100) &&
                !(first == 203 && second == 0 && bytes[2] == 113);
        }
        return (bytes[0] & 0xfe) != 0xfc &&
            !(bytes[0] == 0x20 && bytes[1] == 0x01 && bytes[2] == 0x0d && bytes[3] == 0xb8) &&
            !(bytes[0] == 0x20 && bytes[1] == 0x01 && bytes[2] == 0x00 && bytes[3] == 0x02) &&
            !(bytes[0] == 0x20 && bytes[1] == 0x01 && bytes[2] == 0x00 && bytes[3] == 0x10);
    }
}