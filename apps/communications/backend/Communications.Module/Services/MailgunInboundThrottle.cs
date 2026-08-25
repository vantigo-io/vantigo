using Microsoft.Extensions.Caching.Memory;

namespace Vantigo.Communications.Services;

/// <summary>
/// Bounded in-memory throttle state for the Mailgun inbound webhook. All state
/// lives in one size-limited cache with expiry, so attacker-controlled key
/// cardinality (random channel ids, rotating IPs) cannot grow memory without
/// bound the way the previous static dictionaries could. When the cache is
/// full the limiter degrades to letting requests through rather than serving
/// 429s to legitimate providers; memory stays capped either way.
/// </summary>
internal sealed class MailgunInboundThrottle : IDisposable
{
    private const int MaxEntries = 20_000;

    private readonly MemoryCache _cache = new(new MemoryCacheOptions { SizeLimit = MaxEntries });
    private readonly object _gateCreationLock = new();

    internal int EntryCountForTesting => _cache.Count;

    /// <summary>
    /// Counts a hit in the fixed window for <paramref name="key"/> and reports
    /// whether it is within <paramref name="limit"/>.
    /// </summary>
    public bool TryTake(string key, int limit, TimeSpan window)
    {
        var counter = _cache.GetOrCreate("window|" + key, entry =>
        {
            entry.Size = 1;
            entry.AbsoluteExpirationRelativeToNow = window;
            return new Counter();
        });
        return counter is null || Interlocked.Increment(ref counter.Count) <= limit;
    }

    /// <summary>
    /// Returns the per-key concurrency gate. Creation is serialized so two
    /// concurrent requests for the same key share one semaphore.
    /// </summary>
    public SemaphoreSlim GetGate(string key)
    {
        var cacheKey = "gate|" + key;
        if (_cache.TryGetValue<SemaphoreSlim>(cacheKey, out var existing) && existing is not null)
            return existing;

        lock (_gateCreationLock)
        {
            if (_cache.TryGetValue(cacheKey, out existing) && existing is not null)
                return existing;

            var gate = new SemaphoreSlim(1, 1);
            using var entry = _cache.CreateEntry(cacheKey);
            entry.Size = 1;
            entry.SlidingExpiration = TimeSpan.FromMinutes(5);
            entry.Value = gate;
            return gate;
        }
    }

    /// <summary>
    /// Reports whether the channel is a valid inbound target, caching the
    /// verdict briefly so a flood of requests does not become a database
    /// lookup per request.
    /// </summary>
    public async Task<bool> IsKnownChannelAsync(Guid channelId, Func<Task<bool>> probe)
    {
        var cacheKey = "channel|" + channelId.ToString("N");
        if (_cache.TryGetValue<bool?>(cacheKey, out var known) && known is not null)
            return known.Value;

        var exists = await probe();
        using (var entry = _cache.CreateEntry(cacheKey))
        {
            entry.Size = 1;
            entry.AbsoluteExpirationRelativeToNow = TimeSpan.FromSeconds(exists ? 60 : 30);
            entry.Value = (bool?)exists;
        }

        return exists;
    }

    public void Dispose() => _cache.Dispose();

    private sealed class Counter
    {
        public int Count;
    }
}