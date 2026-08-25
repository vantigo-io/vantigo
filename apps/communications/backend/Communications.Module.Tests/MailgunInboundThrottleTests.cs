using Vantigo.Communications.Services;

namespace Vantigo.Communications.Module.Tests;

public sealed class MailgunInboundThrottleTests
{
    [Fact]
    public void Window_limit_is_enforced_per_key()
    {
        using var throttle = new MailgunInboundThrottle();

        for (var hit = 0; hit < 5; hit++)
            Assert.True(throttle.TryTake("key-a", 5, TimeSpan.FromMinutes(1)));
        Assert.False(throttle.TryTake("key-a", 5, TimeSpan.FromMinutes(1)));

        // Another key has its own window.
        Assert.True(throttle.TryTake("key-b", 5, TimeSpan.FromMinutes(1)));
    }

    [Fact]
    public void State_stays_bounded_under_attacker_controlled_key_cardinality()
    {
        using var throttle = new MailgunInboundThrottle();

        for (var key = 0; key < 100_000; key++)
            throttle.TryTake($"channel-{key}", 20, TimeSpan.FromMinutes(1));

        Assert.InRange(throttle.EntryCountForTesting, 0, 20_000);
    }

    [Fact]
    public void Concurrent_requests_for_one_key_share_a_gate()
    {
        using var throttle = new MailgunInboundThrottle();

        var first = throttle.GetGate("gate-key");
        var second = throttle.GetGate("gate-key");

        Assert.Same(first, second);
        Assert.NotSame(first, throttle.GetGate("other-key"));
    }

    [Fact]
    public async Task Channel_validity_is_cached_instead_of_probed_per_request()
    {
        using var throttle = new MailgunInboundThrottle();
        var channelId = Guid.NewGuid();
        var probes = 0;

        Task<bool> Probe()
        {
            probes++;
            return Task.FromResult(true);
        }

        Assert.True(await throttle.IsKnownChannelAsync(channelId, Probe));
        Assert.True(await throttle.IsKnownChannelAsync(channelId, Probe));

        Assert.Equal(1, probes);
    }

    [Fact]
    public async Task Unknown_channels_are_cached_as_unknown()
    {
        using var throttle = new MailgunInboundThrottle();
        var channelId = Guid.NewGuid();
        var probes = 0;

        Task<bool> Probe()
        {
            probes++;
            return Task.FromResult(false);
        }

        Assert.False(await throttle.IsKnownChannelAsync(channelId, Probe));
        Assert.False(await throttle.IsKnownChannelAsync(channelId, Probe));

        Assert.Equal(1, probes);
    }
}