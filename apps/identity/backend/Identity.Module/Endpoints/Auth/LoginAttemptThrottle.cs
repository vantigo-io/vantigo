using Microsoft.Extensions.Caching.Memory;

namespace Vantigo.Identity.Endpoints.Auth;

/// <summary>
/// Bounded per-account credential throttle for the login endpoint. The IP-only
/// rate-limit policy stays as a broad backstop; this limiter counts failed
/// password attempts per normalized account and client IP, so credential
/// stuffing against one account is cut off far below the IP backstop and
/// independent of how many replicas serve the traffic multiplying that
/// backstop. A successful sign-in clears the account's window, so legitimate
/// users who mistype a few times are not punished after they get it right.
/// The cache is size-limited, so attacker-chosen account-name cardinality
/// cannot grow memory without bound.
/// </summary>
internal sealed class LoginAttemptThrottle : IDisposable
{
    private const int MaxEntries = 50_000;
    private const int MaxFailuresPerWindow = 10;
    private static readonly TimeSpan Window = TimeSpan.FromMinutes(1);

    private readonly MemoryCache _cache = new(new MemoryCacheOptions { SizeLimit = MaxEntries });

    internal int EntryCountForTesting => _cache.Count;

    public bool IsBlocked(string email, string clientIp) =>
        _cache.TryGetValue<Counter>(Key(email, clientIp), out var counter) &&
        counter is not null &&
        Volatile.Read(ref counter.Count) >= MaxFailuresPerWindow;

    public void RecordFailure(string email, string clientIp)
    {
        var counter = _cache.GetOrCreate(Key(email, clientIp), entry =>
        {
            entry.Size = 1;
            entry.AbsoluteExpirationRelativeToNow = Window;
            return new Counter();
        });
        if (counter is not null)
            Interlocked.Increment(ref counter.Count);
    }

    public void RecordSuccess(string email, string clientIp) =>
        _cache.Remove(Key(email, clientIp));

    public void Dispose() => _cache.Dispose();

    private static string Key(string email, string clientIp) =>
        $"{email.Trim().ToUpperInvariant()}|{clientIp}";

    private sealed class Counter
    {
        public int Count;
    }
}