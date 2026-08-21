using System.Net;

using Microsoft.Extensions.Options;

using Vantigo.Communications.Services;
using Vantigo.Configuration;

namespace Vantigo.Communications.Module.Tests.Services;

public sealed class SmtpDestinationGuardTests
{
    [Theory]
    [InlineData("10.0.0.5")]        // RFC1918
    [InlineData("172.16.5.5")]      // RFC1918
    [InlineData("192.168.1.10")]    // RFC1918
    [InlineData("127.0.0.1")]       // loopback
    [InlineData("169.254.169.254")] // cloud metadata address
    [InlineData("169.254.1.1")]     // RFC3927 link-local
    [InlineData("0.0.0.0")]         // "this network"
    [InlineData("::1")]             // IPv6 loopback
    [InlineData("fd12:3456:789a::1")] // RFC4193 unique local
    [InlineData("fe80::1")]         // IPv6 link-local
    public async Task Private_or_reserved_address_is_rejected(string address)
    {
        var guard = CreateGuard(new StubDnsResolver(IPAddress.Parse(address)), new());

        await Assert.ThrowsAsync<SmtpDestinationRejectedException>(() => guard.VetAsync("smtp.internal.test", CancellationToken.None));
    }

    [Fact]
    public async Task Public_address_is_allowed()
    {
        var publicAddress = IPAddress.Parse("93.184.216.34");
        var guard = CreateGuard(new StubDnsResolver(publicAddress), new());

        var vetted = await guard.VetAsync("smtp.example.test", CancellationToken.None);

        Assert.Equal(publicAddress, vetted);
    }

    [Fact]
    public async Task Rebinding_response_is_rejected_at_connect_time()
    {
        // Simulates DNS rebinding: whatever an earlier lookup (e.g. at channel
        // verification time) returned, the resolver used immediately before the
        // connect - which is the only lookup this guard ever trusts - now answers
        // with a private address, and that answer is what gets checked and used.
        var resolver = new StubDnsResolver(IPAddress.Parse("10.10.10.10"));
        var guard = CreateGuard(resolver, new());

        await Assert.ThrowsAsync<SmtpDestinationRejectedException>(() => guard.VetAsync("attacker-controlled.test", CancellationToken.None));
    }

    [Fact]
    public async Task Allowlisted_host_bypasses_private_network_rejection()
    {
        var guard = CreateGuard(new StubDnsResolver(IPAddress.Parse("10.0.0.5")), new() { AllowedHosts = ["internal-relay.corp.test"] });

        var vetted = await guard.VetAsync("internal-relay.corp.test", CancellationToken.None);

        Assert.Equal(IPAddress.Parse("10.0.0.5"), vetted);
    }

    [Fact]
    public async Task Allowlist_does_not_exempt_other_hosts()
    {
        var guard = CreateGuard(new StubDnsResolver(IPAddress.Parse("10.0.0.5")), new() { AllowedHosts = ["internal-relay.corp.test"] });

        await Assert.ThrowsAsync<SmtpDestinationRejectedException>(() => guard.VetAsync("someone-else.test", CancellationToken.None));
    }

    [Fact]
    public async Task AllowPrivateNetworks_permits_private_destinations()
    {
        var guard = CreateGuard(new StubDnsResolver(IPAddress.Parse("172.20.0.5")), new() { AllowPrivateNetworks = true });

        var vetted = await guard.VetAsync("mailhog", CancellationToken.None);

        Assert.Equal(IPAddress.Parse("172.20.0.5"), vetted);
    }

    [Fact]
    public async Task Ip_literal_host_is_checked_without_calling_the_resolver()
    {
        var resolver = new StubDnsResolver(IPAddress.Parse("93.184.216.34"));
        var guard = CreateGuard(resolver, new());

        await Assert.ThrowsAsync<SmtpDestinationRejectedException>(() => guard.VetAsync("127.0.0.1", CancellationToken.None));

        Assert.Equal(0, resolver.CallCount);
    }

    [Fact]
    public async Task Empty_host_is_rejected()
    {
        var guard = CreateGuard(new StubDnsResolver(IPAddress.Parse("93.184.216.34")), new());

        await Assert.ThrowsAsync<SmtpDestinationRejectedException>(() => guard.VetAsync(" ", CancellationToken.None));
    }

    [Fact]
    public async Task No_resolved_addresses_is_rejected()
    {
        var guard = CreateGuard(new StubDnsResolver(), new());

        await Assert.ThrowsAsync<SmtpDestinationRejectedException>(() => guard.VetAsync("nowhere.test", CancellationToken.None));
    }

    private static SmtpDestinationGuard CreateGuard(IDnsResolver resolver, CommunicationsSmtpOptions smtp) =>
        new(resolver, Options.Create(new CommunicationsOptions { Smtp = smtp }));

    private sealed class StubDnsResolver(params IPAddress[] addresses) : IDnsResolver
    {
        public int CallCount { get; private set; }

        public Task<IPAddress[]> ResolveAsync(string host, CancellationToken cancellationToken)
        {
            CallCount++;
            return Task.FromResult(addresses);
        }
    }
}