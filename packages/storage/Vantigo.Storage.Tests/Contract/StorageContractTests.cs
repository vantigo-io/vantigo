using Vantigo.Storage.Abstractions;

namespace Vantigo.Storage.Tests.Contract;

public sealed class StorageContractTests
{
    [Fact]
    public void Public_contract_has_no_provider_presigned_url_operation()
    {
        Assert.DoesNotContain(typeof(IObjectStore).GetMethods(), method => method.Name.Contains("Presigned", StringComparison.OrdinalIgnoreCase));
        Assert.Contains(typeof(IObjectStore).GetMethods(), method => method.Name == nameof(IObjectStore.GetAsync));
        Assert.True(typeof(IStorageScope).IsInterface);
        Assert.True(typeof(IObjectStore<>).IsInterface);
        Assert.DoesNotContain(typeof(IObjectStore).GetProperties(), property => property.PropertyType == typeof(Uri));
    }
}