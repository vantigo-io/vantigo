using System.Text;

using Microsoft.Extensions.Options;

using Testcontainers.Minio;

using Vantigo.Configuration;
using Vantigo.Storage.Providers.S3;

namespace Vantigo.Storage.Tests.Providers.S3;

public sealed class MinioObjectStoreTests : IAsyncLifetime
{
    // MinIO no longer publishes minio/minio on Docker Hub (pulls are denied), so
    // the image comes from MinIO's own registry.
    private readonly MinioContainer container = new MinioBuilder("quay.io/minio/minio")
        .WithUsername("minioadmin")
        .WithPassword("minioadmin")
        .Build();
    private S3ObjectStore? store;

    public async Task InitializeAsync()
    {
        await container.StartAsync();
        var options = new StorageOptions
        {
            Provider = "s3",
            Authentication = "access-key",
            S3 = new S3StorageOptions
            {
                ServiceUrl = container.GetConnectionString(),
                BucketName = "vantigo-objects",
                AccessKey = "minioadmin",
                SecretKey = "minioadmin",
            },
        };
        store = new S3ObjectStore(new OptionsWrapper<StorageOptions>(options));
    }

    public async Task DisposeAsync()
    {
        store?.Dispose();
        await container.DisposeAsync();
    }

    [Fact]
    public async Task Puts_gets_checks_and_deletes_objects()
    {
        var key = $"tests/{Guid.NewGuid():N}.txt";
        await store!.PutAsync(key, new MemoryStream(Encoding.UTF8.GetBytes("hello")), "text/plain");

        Assert.True(await store.ExistsAsync(key));
        await using (var content = await store.GetAsync(key))
        {
            using var reader = new StreamReader(content!);
            Assert.Equal("hello", await reader.ReadToEndAsync());
        }

        await store.DeleteAsync(key);
        Assert.False(await store.ExistsAsync(key));
    }
}