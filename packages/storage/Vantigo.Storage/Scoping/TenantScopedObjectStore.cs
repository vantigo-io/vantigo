using Vantigo.Storage.Abstractions;
using Vantigo.Storage.Initialization;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Storage.Scoping;

/// <summary>Prefixes provider keys with the tenant resolved for the current execution flow.</summary>
internal sealed class TenantScopedObjectStore : IObjectStore
{
    private readonly ITenantContext _tenantContext;
    private readonly IObjectStore _inner;

    public TenantScopedObjectStore(ITenantContext tenantContext, IObjectStore inner)
    {
        _tenantContext = tenantContext ?? throw new ArgumentNullException(nameof(tenantContext));
        _inner = inner ?? throw new ArgumentNullException(nameof(inner));
    }

    public Task PutAsync(string key, Stream content, string contentType, CancellationToken cancellationToken = default) =>
        _inner.PutAsync(PrefixKey(key), content, contentType, cancellationToken);

    public Task<Stream?> GetAsync(string key, CancellationToken cancellationToken = default) =>
        _inner.GetAsync(PrefixKey(key), cancellationToken);

    public Task<bool> ExistsAsync(string key, CancellationToken cancellationToken = default) =>
        _inner.ExistsAsync(PrefixKey(key), cancellationToken);

    public Task DeleteAsync(string key, CancellationToken cancellationToken = default) =>
        _inner.DeleteAsync(PrefixKey(key), cancellationToken);

    private string PrefixKey(string key)
    {
        if (!_tenantContext.IsResolved)
            throw new TenantUnresolvedException();

        var tenantId = _tenantContext.Current;
        if (tenantId.IsEmpty)
            throw new TenantUnresolvedException();

        return $"tenants/{tenantId.Value:D}/{key}";
    }
}

/// <summary>
/// Composes the module scope outside the tenant decorator. This keeps the
/// static scope prefix ahead of the tenant decorator's provider prefix.
/// </summary>
internal sealed class TenantScopedObjectStore<TScope> : IObjectStore<TScope>
    where TScope : IStorageScope
{
    private readonly IObjectStore<TScope> _inner;

    public TenantScopedObjectStore(ITenantContext tenantContext, IStorageBackend backend)
    {
        ArgumentNullException.ThrowIfNull(tenantContext);
        ArgumentNullException.ThrowIfNull(backend);

        _inner = new ScopedObjectStore<TScope>(new TenantScopedObjectStore(tenantContext, backend));
    }

    public Task PutAsync(string key, Stream content, string contentType, CancellationToken cancellationToken = default) =>
        _inner.PutAsync(key, content, contentType, cancellationToken);

    public Task<Stream?> GetAsync(string key, CancellationToken cancellationToken = default) =>
        _inner.GetAsync(key, cancellationToken);

    public Task<bool> ExistsAsync(string key, CancellationToken cancellationToken = default) =>
        _inner.ExistsAsync(key, cancellationToken);

    public Task DeleteAsync(string key, CancellationToken cancellationToken = default) =>
        _inner.DeleteAsync(key, cancellationToken);
}