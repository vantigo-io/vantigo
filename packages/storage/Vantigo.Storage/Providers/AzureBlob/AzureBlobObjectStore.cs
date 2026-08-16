using Azure.Core;
using Azure.Storage.Blobs;
using Azure.Storage.Blobs.Models;

using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Storage.Abstractions;
using Vantigo.Storage.Initialization;
using Vantigo.Storage.Validation;

namespace Vantigo.Storage.Providers.AzureBlob;

/// <summary>Azure Blob Storage implementation using explicit authentication modes.</summary>
internal sealed class AzureBlobObjectStore : IObjectStore, IStorageInitializer, IDisposable
{
    private readonly BlobContainerClient _container;
    private readonly AzureBlobStorageOptions _options;
    private readonly Lazy<Task> _initialization;

    public AzureBlobObjectStore(IOptions<StorageOptions> options)
        : this(options, tokenCredential: null)
    {
    }

    public AzureBlobObjectStore(IOptions<StorageOptions> options, TokenCredential? tokenCredential)
        : this(CreateContainerClient(options.Value, tokenCredential), GetOptions(options.Value))
    {
    }

    private AzureBlobObjectStore(BlobContainerClient container, AzureBlobStorageOptions options)
    {
        this._container = container ?? throw new ArgumentNullException(nameof(container));
        this._options = options ?? throw new ArgumentNullException(nameof(options));
        _initialization = new(InitializeCoreAsync, LazyThreadSafetyMode.ExecutionAndPublication);
    }

    public async Task PutAsync(string key, Stream content, string contentType, CancellationToken cancellationToken = default)
    {
        StorageKey.ValidateProviderKey(key);
        ArgumentNullException.ThrowIfNull(content);
        ArgumentException.ThrowIfNullOrWhiteSpace(contentType);
        await EnsureInitializedAsync(cancellationToken);
        await _container.GetBlobClient(key).UploadAsync(content, new BlobUploadOptions
        {
            HttpHeaders = new BlobHttpHeaders { ContentType = contentType },
        }, cancellationToken);
    }

    public async Task<Stream?> GetAsync(string key, CancellationToken cancellationToken = default)
    {
        StorageKey.ValidateProviderKey(key);
        await EnsureInitializedAsync(cancellationToken);
        try
        {
            var response = await _container.GetBlobClient(key).DownloadStreamingAsync(cancellationToken: cancellationToken);
            return response.Value.Content;
        }
        catch (Azure.RequestFailedException exception) when (exception.Status == 404)
        {
            return null;
        }
    }

    public async Task<bool> ExistsAsync(string key, CancellationToken cancellationToken = default)
    {
        StorageKey.ValidateProviderKey(key);
        await EnsureInitializedAsync(cancellationToken);
        return await _container.GetBlobClient(key).ExistsAsync(cancellationToken);
    }

    public async Task DeleteAsync(string key, CancellationToken cancellationToken = default)
    {
        StorageKey.ValidateProviderKey(key);
        await EnsureInitializedAsync(cancellationToken);
        await _container.DeleteBlobIfExistsAsync(key, cancellationToken: cancellationToken);
    }

    public Task InitializeAsync(CancellationToken cancellationToken = default) => EnsureInitializedAsync(cancellationToken);

    public void Dispose()
    {
        // Blob clients do not own disposable transport in the Azure SDK.
    }

    private async Task EnsureInitializedAsync(CancellationToken cancellationToken)
    {
        cancellationToken.ThrowIfCancellationRequested();
        await _initialization.Value.WaitAsync(cancellationToken);
    }

    private async Task InitializeCoreAsync()
    {
        if (_options.CreateContainerIfMissing)
        {
            await _container.CreateIfNotExistsAsync();
            return;
        }

        // Verify the configured container without listing containers or attempting creation.
        await _container.GetPropertiesAsync();
    }

    private static BlobContainerClient CreateContainerClient(StorageOptions options, TokenCredential? tokenCredential)
    {
        options.Validate();
        var azure = options.AzureBlob;
        var service = options.Authentication switch
        {
            "azure-identity" => new BlobServiceClient(
                new Uri($"https://{azure.AccountName}.blob.core.windows.net"),
                tokenCredential ?? throw StorageOptions.Invalid(
                    "Azure Blob azure-identity authentication requires the global Azure identity TokenCredential. " +
                    "Enable AZURE__IDENTITY__ENABLED or choose connection-string or sas authentication.")),
            "connection-string" => new BlobServiceClient(azure.ConnectionString),
            "sas" => new BlobServiceClient(CreateSasUri(azure)),
            _ => throw StorageOptions.Invalid("Authentication must be an Azure Blob authentication mode.")
        };
        return service.GetBlobContainerClient(azure.ContainerName);
    }

    private static Uri CreateSasUri(AzureBlobStorageOptions options)
    {
        var token = options.SasToken!.TrimStart('?');
        if (token.Length == 0 || token.Any(char.IsControl) || token.Contains('#') || token.Contains('?'))
            throw StorageOptions.Invalid("AzureBlob:SasToken is not a valid SAS query token.");
        return new Uri($"https://{options.AccountName}.blob.core.windows.net/?{token}", UriKind.Absolute);
    }

    private static AzureBlobStorageOptions GetOptions(StorageOptions options)
    {
        options.Validate();
        return options.AzureBlob;
    }
}