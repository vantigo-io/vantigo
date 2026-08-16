namespace Vantigo.Storage.Abstractions;

/// <summary>
/// Names the isolated logical namespace owned by a module.
/// </summary>
public interface IStorageScope
{
    /// <summary>The canonical, lowercase namespace name.</summary>
    static abstract string Name { get; }
}

/// <summary>Stores and retrieves opaque objects using a storage key.</summary>
public interface IObjectStore
{
    /// <summary>Stores content. The caller owns and disposes <paramref name="content"/>.</summary>
    Task PutAsync(string key, Stream content, string contentType, CancellationToken cancellationToken = default);

    /// <summary>Gets content, or null when the key does not exist.</summary>
    /// <remarks>The caller owns and disposes the returned stream.</remarks>
    Task<Stream?> GetAsync(string key, CancellationToken cancellationToken = default);

    Task<bool> ExistsAsync(string key, CancellationToken cancellationToken = default);

    /// <summary>Deletes content. Deleting a missing key is successful.</summary>
    Task DeleteAsync(string key, CancellationToken cancellationToken = default);
}

/// <summary>Stores objects in the namespace declared by <typeparamref name="TScope"/>.</summary>
public interface IObjectStore<TScope> : IObjectStore where TScope : IStorageScope;