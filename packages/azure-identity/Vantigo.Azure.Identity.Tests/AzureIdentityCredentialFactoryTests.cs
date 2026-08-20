using global::Azure.Identity;

using Vantigo.Azure.Identity;

namespace Vantigo.Azure.Identity.Tests;

public sealed class AzureIdentityCredentialFactoryTests
{
    [Fact]
    public void Create_returns_a_default_azure_credential()
    {
        var credential = AzureIdentityCredentialFactory.Create();

        Assert.IsType<DefaultAzureCredential>(credential);
    }

    [Fact]
    public void Create_returns_a_new_instance_each_call()
    {
        var first = AzureIdentityCredentialFactory.Create();
        var second = AzureIdentityCredentialFactory.Create();

        Assert.NotSame(first, second);
    }
}