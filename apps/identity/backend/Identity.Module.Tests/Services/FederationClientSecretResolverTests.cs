using Microsoft.Extensions.Configuration;

using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Services;

public sealed class FederationClientSecretResolverTests
{
    [Fact]
    public void ResolvesOnlyDedicatedEnvironmentNamespaceWithoutExposingItForMissingOrInvalidKeys()
    {
        var configuration = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["VANTIGO_SSO_ENTRA_CLIENT_SECRET"] = "secret-that-must-not-be-returned-by-apis",
                ["ConnectionStrings:Vantigo"] = "secret-from-arbitrary-config",
                ["VANTIGO_SSO_ALIAS"] = "secret-from-application-alias",
            })
            .Build();
        var resolver = new ConfigurationFederationClientSecretResolver(configuration);

        var variableName = "VANTIGO_SSO_ENTRA_CLIENT_SECRET";
        var previous = Environment.GetEnvironmentVariable(variableName);
        Environment.SetEnvironmentVariable(variableName, "secret-that-must-not-be-returned-by-apis");
        try
        {
            var configured = resolver.Resolve(variableName);
            Assert.Equal(FederationClientSecretResolutionStatus.Configured, configured.Status);
            Assert.Equal("secret-that-must-not-be-returned-by-apis", configured.Secret);
        }
        finally
        {
            Environment.SetEnvironmentVariable(variableName, previous);
        }
        Assert.Equal(FederationClientSecretResolutionStatus.Missing, resolver.Resolve("VANTIGO_SSO_MISSING_CLIENT_SECRET").Status);
        Assert.Equal(FederationClientSecretResolutionStatus.InvalidReference, resolver.Resolve("ConnectionStrings:Vantigo").Status);
        Assert.Equal(FederationClientSecretResolutionStatus.InvalidReference, resolver.Resolve("VANTIGO_SSO_ALIAS").Status);
        Assert.Equal(FederationClientSecretResolutionStatus.InvalidReference, resolver.Resolve("VANTIGO_SSO_ENTRA_CLIENT_SECRET ").Status);
        Assert.Equal(FederationClientSecretResolutionStatus.InvalidReference, resolver.Resolve("VANTIGO_SSO_SECRET=bad").Status);
        Assert.Equal(FederationClientSecretResolutionStatus.InvalidReference, resolver.Resolve("VANTIGO_SSO__CLIENT_SECRET").Status);
    }
}