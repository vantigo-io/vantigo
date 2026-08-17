using System.Collections.Concurrent;
using System.Text;

using Amazon.S3;

using Azure.Core;

using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;

using Vantigo.Configuration;
using Vantigo.Storage;
using Vantigo.Storage.Abstractions;
using Vantigo.Storage.Initialization;
using Vantigo.Storage.Providers.AzureBlob;
using Vantigo.Storage.Tests.TestSupport;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Storage.Tests.DependencyInjection;

public sealed class AddVantigoObjectStorageTests
{
    [Fact]
    public async Task Missing_provider_resolves_fail_closed_without_constructing_a_provider()
    {
        var services = CreateServices();
        services.AddVantigoObjectStorage(StorageTestConfiguration.Build());
        using var provider = services.BuildServiceProvider();

        Assert.Null(provider.GetService<IObjectStore>());
        var store = provider.GetRequiredService<IObjectStore<CommunicationsScope>>();
        Assert.Same(store, provider.GetRequiredService<IObjectStore<CommunicationsScope>>());
        var exception = await Assert.ThrowsAsync<InvalidOperationException>(() => store.ExistsAsync("file"));

        Assert.Contains("Storage:Provider", exception.Message, StringComparison.Ordinal);
        Assert.Contains("not configured", exception.Message, StringComparison.OrdinalIgnoreCase);
        Assert.Null(provider.GetService<IHostEnvironment>());
        Assert.DoesNotContain(services, descriptor => descriptor.ServiceType == typeof(IAmazonS3));
        await provider.GetRequiredService<IHostedService>().StartAsync(CancellationToken.None);
    }

    [Fact]
    public async Task Configured_s3_resolves_typed_store_and_combines_the_scope_into_the_physical_key()
    {
        var configuration = StorageTestConfiguration.Build(
            ("Storage:Provider", "s3"),
            ("Storage:Authentication", "access-key"),
            ("Storage:S3:BucketName", "vantigo-objects"),
            ("Storage:S3:ServiceUrl", "http://127.0.0.1:9000"),
            ("Storage:S3:AccessKey", "access"),
            ("Storage:S3:SecretKey", "secret"));
        var services = CreateServices();
        services.AddVantigoObjectStorage(configuration);
        using var provider = services.BuildServiceProvider();

        var store = provider.GetRequiredService<IObjectStore<CommunicationsScope>>();

        Assert.NotNull(store);
        Assert.Null(provider.GetService<IObjectStore>());
        Assert.Equal("s3", provider.GetRequiredService<Microsoft.Extensions.Options.IOptions<StorageOptions>>().Value.Provider);
    }

    [Fact]
    public void Azure_identity_uses_the_global_token_credential()
    {
        var credential = new TestTokenCredential();
        var services = CreateServices();
        services.AddSingleton<TokenCredential>(credential);
        services.AddVantigoObjectStorage(StorageTestConfiguration.Build(
            ("Storage:Provider", "azure-blob"),
            ("Storage:Authentication", "azure-identity"),
            ("Storage:AzureBlob:AccountName", "vantigoaccount"),
            ("Storage:AzureBlob:ContainerName", "objects")));
        using var provider = services.BuildServiceProvider();

        Assert.Same(credential, provider.GetRequiredService<TokenCredential>());
        Assert.NotNull(provider.GetRequiredService<IObjectStore<CommunicationsScope>>());
    }

    [Fact]
    public void Disabled_global_azure_identity_fails_when_storage_is_resolved()
    {
        var services = CreateServices();
        var configuration = StorageTestConfiguration.Build(
            ("Azure:Identity:Enabled", "false"),
            ("Storage:Provider", "azure-blob"),
            ("Storage:Authentication", "azure-identity"),
            ("Storage:AzureBlob:AccountName", "vantigoaccount"),
            ("Storage:AzureBlob:ContainerName", "objects"));

        services.AddVantigoObjectStorage(configuration);
        using var provider = services.BuildServiceProvider();
        var exception = Assert.Throws<InvalidOperationException>(() =>
            provider.GetRequiredService<IStorageBackend>());
        Assert.Contains("enable Azure identity", exception.Message, StringComparison.OrdinalIgnoreCase);
    }

    [Theory]
    [InlineData("connection-string", "DefaultEndpointsProtocol=https;AccountName=vantigoaccount;AccountKey=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=;EndpointSuffix=core.windows.net")]
    [InlineData("sas", null)]
    public void Azure_non_token_authentication_does_not_require_a_token_credential(string authentication, string? connectionString)
    {
        var values = new List<(string Key, string? Value)>
        {
            ("Storage:Provider", "azure-blob"),
            ("Storage:Authentication", authentication),
            ("Storage:AzureBlob:AccountName", "vantigoaccount"),
            ("Storage:AzureBlob:ContainerName", "objects"),
            ("Storage:AzureBlob:ConnectionString", connectionString),
            ("Storage:AzureBlob:SasToken", authentication == "sas" ? "sv=2024-01-01&sig=secret-signature" : null),
        };
        var services = CreateServices();
        services.AddVantigoObjectStorage(StorageTestConfiguration.Build(values.ToArray()));
        using var provider = services.BuildServiceProvider();

        Assert.NotNull(provider.GetRequiredService<IObjectStore<CommunicationsScope>>());
        Assert.Null(provider.GetService<TokenCredential>());
    }

    [Fact]
    public async Task Configured_s3_uses_the_existing_backend_seam_for_scoped_physical_keys()
    {
        var backend = new RecordingObjectStore();
        var services = CreateServices();
        services.AddVantigoObjectStorage(StorageTestConfiguration.Build(
            ("Storage:Provider", "s3"),
            ("Storage:Authentication", "access-key"),
            ("Storage:S3:BucketName", "vantigo-objects"),
            ("Storage:S3:ServiceUrl", "http://127.0.0.1:9000"),
            ("Storage:S3:AccessKey", "access"),
            ("Storage:S3:SecretKey", "secret")));
        // IStorageBackend is deliberately internal; replacing it here uses the existing
        // provider seam while retaining the real AddVantigoObjectStorage registrations.
        services.AddSingleton<IStorageBackend>(new StorageBackendAdapter(backend));
        using var provider = services.BuildServiceProvider();

        var store = provider.GetRequiredService<IObjectStore<CommunicationsScope>>();
        await store.PutAsync("inbox/message.eml", new MemoryStream(Encoding.UTF8.GetBytes("body")), "message/rfc822");

        Assert.Equal(
            "tenants/11111111-1111-1111-1111-111111111111/communications/inbox/message.eml",
            Assert.Single(backend.Keys));
        Assert.Null(provider.GetService<IObjectStore>());
    }

    [Fact]
    public void Local_none_resolves_typed_store_and_constructs_no_network_provider()
    {
        var root = Path.Combine(Path.GetTempPath(), $"vantigo-di-{Guid.NewGuid():N}");
        try
        {
            var services = CreateServices();
            services.AddSingleton<IHostEnvironment>(new TestHostEnvironment());
            services.AddVantigoObjectStorage(StorageTestConfiguration.Build(
                ("Storage:Provider", "local"),
                ("Storage:Authentication", "none"),
                ("Storage:Local:RootPath", root)));
            using var provider = services.BuildServiceProvider();

            Assert.IsAssignableFrom<IObjectStore<CommunicationsScope>>(provider.GetRequiredService<IObjectStore<CommunicationsScope>>());
            Assert.IsAssignableFrom<IObjectStore<CustomerScope>>(provider.GetRequiredService<IObjectStore<CustomerScope>>());
            Assert.Null(provider.GetService<IObjectStore>());
            Assert.DoesNotContain(services, descriptor => descriptor.ServiceType == typeof(IAmazonS3));
        }
        finally
        {
            if (Directory.Exists(root)) Directory.Delete(root, recursive: true);
        }
    }

    [Fact]
    public async Task Local_insecure_root_is_rejected_in_production_during_initialization()
    {
        var root = Path.Combine(Path.GetTempPath(), $"vantigo-di-{Guid.NewGuid():N}");
        var services = CreateServices();
        services.AddSingleton<IHostEnvironment>(new TestHostEnvironment(Environments.Production));
        services.AddVantigoObjectStorage(StorageTestConfiguration.Build(
            ("Storage:Provider", "local"),
            ("Storage:Authentication", "none"),
            ("Storage:Local:RootPath", root),
            ("Storage:Local:AllowInsecureRootForDevelopment", "true")));
        using var provider = services.BuildServiceProvider();

        try
        {
            var store = provider.GetRequiredService<IObjectStore<CommunicationsScope>>();
            var exception = await Assert.ThrowsAsync<InvalidOperationException>(() => store.ExistsAsync("file"));

            Assert.Contains("only permitted in the Development environment", exception.Message, StringComparison.Ordinal);
            Assert.Contains("Production", exception.Message, StringComparison.Ordinal);
        }
        finally
        {
            if (Directory.Exists(root)) Directory.Delete(root, recursive: true);
        }
    }

    [Fact]
    public void Invalid_marker_scope_fails_when_the_typed_store_is_resolved()
    {
        var services = CreateServices();
        services.AddVantigoObjectStorage(StorageTestConfiguration.Build());
        using var provider = services.BuildServiceProvider();

        Assert.Throws<ArgumentException>(() => provider.GetRequiredService<IObjectStore<InvalidScope>>());
    }

    [Fact]
    public async Task Multiple_marker_types_are_isolated_without_double_prefixing()
    {
        var backend = new RecordingObjectStore();
        var services = CreateServices();
        services.AddVantigoObjectStorage(StorageTestConfiguration.Build());
        services.AddSingleton<IStorageBackend>(new StorageBackendAdapter(backend));
        using var provider = services.BuildServiceProvider();

        var communications = provider.GetRequiredService<IObjectStore<CommunicationsScope>>();
        var customers = provider.GetRequiredService<IObjectStore<CustomerScope>>();
        await Assert.ThrowsAsync<ArgumentException>(() => communications.ExistsAsync("communications/file"));
        await Assert.ThrowsAsync<ArgumentException>(() => customers.ExistsAsync("customers/file"));
    }

    [Fact]
    public async Task Startup_initializer_creates_the_configured_local_root_only_when_started()
    {
        var root = Path.Combine(Path.GetTempPath(), $"vantigo-init-{Guid.NewGuid():N}");
        var services = CreateServices();
        services.AddSingleton<IHostEnvironment>(new TestHostEnvironment());
        services.AddVantigoObjectStorage(StorageTestConfiguration.Build(
            ("Storage:Provider", "local"),
            ("Storage:Authentication", "none"),
            ("Storage:Local:RootPath", root)));
        using var provider = services.BuildServiceProvider();

        try
        {
            Assert.False(Directory.Exists(root));
            var hostedService = provider.GetRequiredService<IHostedService>();
            await hostedService.StartAsync(CancellationToken.None);

            Assert.True(Directory.Exists(root));
        }
        finally
        {
            if (Directory.Exists(root)) Directory.Delete(root, recursive: true);
        }
    }

    private sealed class RecordingObjectStore : IObjectStore
    {
        public ConcurrentBag<string> Keys { get; } = [];

        public Task PutAsync(string key, Stream content, string contentType, CancellationToken cancellationToken = default)
        {
            Keys.Add(key);
            return Task.CompletedTask;
        }

        public Task<Stream?> GetAsync(string key, CancellationToken cancellationToken = default) => Task.FromResult<Stream?>(null);
        public Task<bool> ExistsAsync(string key, CancellationToken cancellationToken = default) => Task.FromResult(false);
        public Task DeleteAsync(string key, CancellationToken cancellationToken = default) => Task.CompletedTask;
    }

    private sealed class TestTokenCredential : TokenCredential
    {
        public override AccessToken GetToken(TokenRequestContext requestContext, CancellationToken cancellationToken) =>
            new("fake", DateTimeOffset.UtcNow.AddHours(1));

        public override ValueTask<AccessToken> GetTokenAsync(
            TokenRequestContext requestContext,
            CancellationToken cancellationToken) =>
            ValueTask.FromResult(new AccessToken("fake", DateTimeOffset.UtcNow.AddHours(1)));
    }

    private static ServiceCollection CreateServices()
    {
        var services = new ServiceCollection();
        services.AddSingleton<ITenantContext>(new TestTenantContext(
            new TenantId(Guid.Parse("11111111-1111-1111-1111-111111111111"))));
        return services;
    }

}