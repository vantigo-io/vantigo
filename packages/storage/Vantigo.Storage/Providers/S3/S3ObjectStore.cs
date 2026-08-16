using Amazon;
using Amazon.S3;
using Amazon.S3.Model;
using Amazon.S3.Util;

using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Storage.Abstractions;
using Vantigo.Storage.Initialization;
using Vantigo.Storage.Validation;

namespace Vantigo.Storage.Providers.S3;

/// <summary>S3 and S3-compatible object storage implementation.</summary>
internal sealed class S3ObjectStore : IObjectStore, IStorageInitializer, IDisposable
{
    private readonly IAmazonS3 _client;
    private readonly S3StorageOptions _options;
    private readonly Lazy<Task> _bucketInitialization;

    public S3ObjectStore(IOptions<StorageOptions> options)
        : this(CreateClient(options.Value), GetOptions(options.Value))
    {
    }

    private S3ObjectStore(IAmazonS3 client, S3StorageOptions options)
    {
        this._client = client ?? throw new ArgumentNullException(nameof(client));
        this._options = options ?? throw new ArgumentNullException(nameof(options));
        _bucketInitialization = new(EnsureBucketCoreAsync, LazyThreadSafetyMode.ExecutionAndPublication);
    }

    public async Task PutAsync(string key, Stream content, string contentType, CancellationToken cancellationToken = default)
    {
        StorageKey.ValidateProviderKey(key);
        ArgumentNullException.ThrowIfNull(content);
        ArgumentException.ThrowIfNullOrWhiteSpace(contentType);
        await EnsureBucketAsync(cancellationToken);
        await _client.PutObjectAsync(new PutObjectRequest
        {
            BucketName = _options.BucketName,
            Key = key,
            InputStream = content,
            ContentType = contentType,
            AutoCloseStream = false,
            AutoResetStreamPosition = false,
        }, cancellationToken);
    }

    public async Task<Stream?> GetAsync(string key, CancellationToken cancellationToken = default)
    {
        StorageKey.ValidateProviderKey(key);
        await EnsureBucketAsync(cancellationToken);
        try
        {
            var response = await _client.GetObjectAsync(
                new GetObjectRequest { BucketName = _options.BucketName, Key = key }, cancellationToken);
            return response.ResponseStream;
        }
        catch (AmazonS3Exception exception) when (IsNotFound(exception))
        {
            return null;
        }
    }

    public async Task<bool> ExistsAsync(string key, CancellationToken cancellationToken = default)
    {
        StorageKey.ValidateProviderKey(key);
        await EnsureBucketAsync(cancellationToken);
        try
        {
            await _client.GetObjectMetadataAsync(
                new GetObjectMetadataRequest { BucketName = _options.BucketName, Key = key }, cancellationToken);
            return true;
        }
        catch (AmazonS3Exception exception) when (IsNotFound(exception))
        {
            return false;
        }
    }

    public async Task DeleteAsync(string key, CancellationToken cancellationToken = default)
    {
        StorageKey.ValidateProviderKey(key);
        await EnsureBucketAsync(cancellationToken);
        await _client.DeleteObjectAsync(
            new DeleteObjectRequest { BucketName = _options.BucketName, Key = key }, cancellationToken);
    }

    public Task InitializeAsync(CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        return _bucketInitialization.Value;
    }

    public void Dispose() => _client.Dispose();

    private Task EnsureBucketAsync(CancellationToken cancellationToken)
    {
        cancellationToken.ThrowIfCancellationRequested();
        return _bucketInitialization.Value;
    }

    private async Task EnsureBucketCoreAsync()
    {
        if (await AmazonS3Util.DoesS3BucketExistV2Async(_client, _options.BucketName)) return;
        try
        {
            await _client.PutBucketAsync(new PutBucketRequest { BucketName = _options.BucketName });
        }
        catch (AmazonS3Exception exception) when (
            exception.StatusCode == System.Net.HttpStatusCode.Conflict ||
            string.Equals(exception.ErrorCode, "BucketAlreadyOwnedByYou", StringComparison.OrdinalIgnoreCase) ||
            string.Equals(exception.ErrorCode, "BucketAlreadyExists", StringComparison.OrdinalIgnoreCase))
        {
            // Another process may have created it after the existence check.
        }
    }

    private static IAmazonS3 CreateClient(StorageOptions options)
    {
        options.Validate();
        var s3 = options.S3;
        var config = new AmazonS3Config
        {
            ServiceURL = s3.ServiceUrl,
            ForcePathStyle = s3.ForcePathStyle,
        };
        if (string.IsNullOrWhiteSpace(s3.ServiceUrl))
            config.RegionEndpoint = RegionEndpoint.GetBySystemName(s3.Region);
        return new AmazonS3Client(s3.AccessKey, s3.SecretKey, config);
    }

    private static S3StorageOptions GetOptions(StorageOptions options)
    {
        options.Validate();
        return options.S3;
    }

    private static bool IsNotFound(AmazonS3Exception exception) =>
        exception.StatusCode == System.Net.HttpStatusCode.NotFound ||
        string.Equals(exception.ErrorCode, "NoSuchKey", StringComparison.OrdinalIgnoreCase) ||
        string.Equals(exception.ErrorCode, "NotFound", StringComparison.OrdinalIgnoreCase);
}