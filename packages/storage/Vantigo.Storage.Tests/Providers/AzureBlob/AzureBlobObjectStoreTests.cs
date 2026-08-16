using System.Reflection;

using Azure.Storage.Blobs;

using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Storage.Providers.AzureBlob;

namespace Vantigo.Storage.Tests.Providers.AzureBlob;

public sealed class AzureBlobObjectStoreTests
{
    [Fact]
    public void Azure_identity_builds_account_container_client_with_injected_credential_without_network_access()
    {
        var credential = new TestTokenCredential();
        using var store = new AzureBlobObjectStore(
            Options.Create(CreateOptions("azure-identity")),
            credential);
        var container = GetContainer(store);

        Assert.Equal("https://vantigoaccount.blob.core.windows.net/objects", container.Uri.AbsoluteUri);
        Assert.False(container.CanGenerateSasUri);
    }

    [Fact]
    public void Connection_string_builds_shared_key_container_client_without_network_access()
    {
        using var store = new AzureBlobObjectStore(Options.Create(CreateOptions(
            "connection-string",
            connectionString: "DefaultEndpointsProtocol=https;AccountName=vantigoaccount;AccountKey=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=;EndpointSuffix=core.windows.net")));
        var container = GetContainer(store);

        Assert.Equal("https://vantigoaccount.blob.core.windows.net/objects", container.Uri.AbsoluteUri);
        Assert.True(container.CanGenerateSasUri);
    }

    [Fact]
    public void Sas_builds_sas_container_client_without_network_access_and_does_not_expose_token_in_test_failures()
    {
        using var store = new AzureBlobObjectStore(Options.Create(CreateOptions("sas", sasToken: "sv=2024-01-01&sig=secret-signature")));
        var container = GetContainer(store);

        Assert.Equal("https://vantigoaccount.blob.core.windows.net/objects", container.Uri.GetLeftPart(UriPartial.Path));
        Assert.Contains("sig=", container.Uri.Query, StringComparison.Ordinal);
        Assert.False(container.CanGenerateSasUri);
    }

    private static StorageOptions CreateOptions(string authentication,
        string? connectionString = null, string? sasToken = null) => new()
        {
            Provider = "azure-blob",
            Authentication = authentication,
            AzureBlob = new AzureBlobStorageOptions
            {
                AccountName = "vantigoaccount",
                ContainerName = "objects",
                ConnectionString = connectionString,
                SasToken = sasToken,
            },
        };

    private static BlobContainerClient GetContainer(AzureBlobObjectStore store)
    {
        return (BlobContainerClient)typeof(AzureBlobObjectStore)
            .GetField("_container", BindingFlags.Instance | BindingFlags.NonPublic)!
            .GetValue(store)!;
    }

    private sealed class TestTokenCredential : global::Azure.Core.TokenCredential
    {
        public override global::Azure.Core.AccessToken GetToken(
            global::Azure.Core.TokenRequestContext requestContext,
            CancellationToken cancellationToken) =>
            new("fake", DateTimeOffset.UtcNow.AddHours(1));

        public override ValueTask<global::Azure.Core.AccessToken> GetTokenAsync(
            global::Azure.Core.TokenRequestContext requestContext,
            CancellationToken cancellationToken) =>
            ValueTask.FromResult(new global::Azure.Core.AccessToken("fake", DateTimeOffset.UtcNow.AddHours(1)));
    }
}