using Vantigo.Storage.Abstractions;

namespace Vantigo.Storage.Initialization;

// This abstraction deliberately remains internal. Application modules can resolve only
// IObjectStore<TScope>, never the provider or an unscoped backend.
internal interface IStorageBackend : IObjectStore
{
}

internal sealed class StorageBackendAdapter(IObjectStore implementation) : IStorageBackend, IStorageInitializer, IDisposable
{
    private readonly IObjectStore implementation = implementation ?? throw new ArgumentNullException(nameof(implementation));

    public Task PutAsync(string key, Stream content, string contentType, CancellationToken cancellationToken = default) =>
        implementation.PutAsync(key, content, contentType, cancellationToken);

    public Task<Stream?> GetAsync(string key, CancellationToken cancellationToken = default) =>
        implementation.GetAsync(key, cancellationToken);

    public Task<bool> ExistsAsync(string key, CancellationToken cancellationToken = default) =>
        implementation.ExistsAsync(key, cancellationToken);

    public Task DeleteAsync(string key, CancellationToken cancellationToken = default) =>
        implementation.DeleteAsync(key, cancellationToken);

    public Task InitializeAsync(CancellationToken cancellationToken = default) =>
        implementation is IStorageInitializer initializer
            ? initializer.InitializeAsync(cancellationToken)
            : Task.CompletedTask;

    public void Dispose()
    {
        if (implementation is IDisposable disposable)
            disposable.Dispose();
    }
}

internal interface IStorageInitializer
{
    Task InitializeAsync(CancellationToken cancellationToken = default);
}