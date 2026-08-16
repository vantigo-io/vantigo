using Vantigo.Storage.Abstractions;
using Vantigo.Storage.Validation;

namespace Vantigo.Storage.Scoping;

/// <summary>An object store permanently isolated below one marker-type scope.</summary>
internal sealed class ScopedObjectStore<TScope> : IObjectStore<TScope>
    where TScope : IStorageScope
{
    private readonly IObjectStore _inner;
    private readonly string _scope;

    public ScopedObjectStore(IObjectStore inner)
    {
        this._inner = inner ?? throw new ArgumentNullException(nameof(inner));
        _scope = StorageKey.ValidateScope(TScope.Name);
    }

    public Task PutAsync(string key, Stream content, string contentType, CancellationToken cancellationToken = default) =>
        _inner.PutAsync(StorageKey.Combine(_scope, key), content, contentType, cancellationToken);

    public Task<Stream?> GetAsync(string key, CancellationToken cancellationToken = default) =>
        _inner.GetAsync(StorageKey.Combine(_scope, key), cancellationToken);

    public Task<bool> ExistsAsync(string key, CancellationToken cancellationToken = default) =>
        _inner.ExistsAsync(StorageKey.Combine(_scope, key), cancellationToken);

    public Task DeleteAsync(string key, CancellationToken cancellationToken = default) =>
        _inner.DeleteAsync(StorageKey.Combine(_scope, key), cancellationToken);
}