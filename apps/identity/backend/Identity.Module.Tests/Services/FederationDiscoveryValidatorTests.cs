using System.Net;
using System.Net.Http.Headers;

using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Services;

public sealed class FederationDiscoveryValidatorTests
{
    [Fact]
    public async Task ValidatesIssuerAndUsesTheNormalizedDiscoveryEndpoint()
    {
        var handler = new StubHandler(_ => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent("{\"issuer\":\"https://issuer.example.test/tenant\",\"authorization_endpoint\":\"https://issuer.example.test/authorize\",\"token_endpoint\":\"https://issuer.example.test/token\",\"jwks_uri\":\"https://issuer.example.test/keys\"}")
            {
                Headers = { ContentType = new MediaTypeHeaderValue("application/json") },
            },
        });
        var validator = CreateValidator(handler, IPAddress.Parse("1.2.3.4"));

        var result = await validator.ValidateAsync(
            FederationProviderKind.Generic,
            "https://issuer.example.test/tenant",
            CancellationToken.None);

        Assert.True(result.Succeeded);
        Assert.Equal("https://issuer.example.test/tenant/.well-known/openid-configuration", handler.RequestedUri!.ToString());
        Assert.Equal("https://issuer.example.test/tenant", result.Issuer);
        Assert.Equal("https://issuer.example.test/authorize", result.AuthorizationEndpoint);
        Assert.Equal("https://issuer.example.test/token", result.TokenEndpoint);
        Assert.Equal("https://issuer.example.test/keys", result.JwksUri);
    }

    [Fact]
    public async Task RejectsIssuerMismatchRedirectsAndPrivateHosts()
    {
        var mismatchHandler = new StubHandler(_ => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = JsonContent("{\"issuer\":\"https://other.example.test\",\"authorization_endpoint\":\"https://issuer.example.test/authorize\",\"token_endpoint\":\"https://issuer.example.test/token\",\"jwks_uri\":\"https://issuer.example.test/keys\"}"),
        });
        var mismatch = await CreateValidator(mismatchHandler, IPAddress.Parse("1.2.3.4"))
            .ValidateAsync(FederationProviderKind.Generic, "https://issuer.example.test", CancellationToken.None);
        Assert.False(mismatch.Succeeded);
        Assert.Equal("discovery_issuer_mismatch", mismatch.Code);

        var redirect = await CreateValidator(
                new StubHandler(_ => new HttpResponseMessage(HttpStatusCode.Redirect)),
                IPAddress.Parse("1.2.3.4"))
            .ValidateAsync(FederationProviderKind.Generic, "https://issuer.example.test", CancellationToken.None);
        Assert.False(redirect.Succeeded);
        Assert.Equal("discovery_redirect_not_allowed", redirect.Code);

        var privateHost = await CreateValidator(
                new StubHandler(_ => throw new InvalidOperationException("must not request a private host")),
                IPAddress.Parse("127.0.0.1"))
            .ValidateAsync(FederationProviderKind.Generic, "https://issuer.example.test", CancellationToken.None);
        Assert.False(privateHost.Succeeded);
        Assert.Equal("discovery_host_not_public", privateHost.Code);
    }

    [Theory]
    [InlineData("https://login.microsoftonline.com/common/v2.0")]
    [InlineData("https://login.microsoftonline.com/organizations/v2.0")]
    [InlineData("https://login.microsoftonline.com/consumers/v2.0")]
    public async Task RejectsNonTenantSpecificEntraAuthorities(string authority)
    {
        var result = await CreateValidator(
                new StubHandler(_ => throw new InvalidOperationException("must reject before request")),
                IPAddress.Parse("1.2.3.4"))
            .ValidateAsync(FederationProviderKind.Entra, authority, CancellationToken.None);

        Assert.False(result.Succeeded);
        Assert.Equal("provider_authority_mismatch", result.Code);
    }

    [Fact]
    public async Task EnforcesGoogleIssuerConstraintAndJsonContentType()
    {
        var google = await CreateValidator(
                new StubHandler(_ => throw new InvalidOperationException("must reject before request")),
                IPAddress.Parse("1.2.3.4"))
            .ValidateAsync(FederationProviderKind.Google, "https://accounts.google.com/workspace", CancellationToken.None);
        Assert.Equal("provider_authority_mismatch", google.Code);

        var contentType = await CreateValidator(
                new StubHandler(_ => new HttpResponseMessage(HttpStatusCode.OK)
                {
                    Content = new StringContent("{\"issuer\":\"https://issuer.example.test\",\"authorization_endpoint\":\"https://issuer.example.test/authorize\",\"token_endpoint\":\"https://issuer.example.test/token\",\"jwks_uri\":\"https://issuer.example.test/keys\"}"),
                }),
                IPAddress.Parse("1.2.3.4"))
            .ValidateAsync(FederationProviderKind.Generic, "https://issuer.example.test", CancellationToken.None);
        Assert.False(contentType.Succeeded);
        Assert.Equal("discovery_content_type_invalid", contentType.Code);
    }

    [Theory]
    [InlineData("authorization_endpoint")]
    [InlineData("token_endpoint")]
    [InlineData("jwks_uri")]
    public async Task RequiresSafeMetadataEndpoints(string propertyName)
    {
        var metadata = propertyName == "authorization_endpoint"
            ? "https://issuer.example.test/authorize"
            : "https://issuer.example.test/" + propertyName;
        var json = $"{{\"issuer\":\"https://issuer.example.test\",\"authorization_endpoint\":\"https://issuer.example.test/authorize\",\"token_endpoint\":\"https://issuer.example.test/token\",\"jwks_uri\":\"https://issuer.example.test/keys\",\"{propertyName}\":\"http://127.0.0.1/private\"}}";
        var result = await CreateValidator(
                new StubHandler(_ => new HttpResponseMessage(HttpStatusCode.OK) { Content = JsonContent(json) }),
                IPAddress.Parse("1.2.3.4"))
            .ValidateAsync(FederationProviderKind.Generic, "https://issuer.example.test", CancellationToken.None);

        Assert.False(result.Succeeded);
        Assert.Equal("discovery_metadata_endpoint_invalid", result.Code);
    }

    [Theory]
    [InlineData("https://issuer.example.test/")]
    [InlineData("https://ISSUER.EXAMPLE.TEST")]
    public async Task RequiresExactDiscoveryIssuerEquality(string discoveredIssuer)
    {
        var json = $"{{\"issuer\":\"{discoveredIssuer}\",\"authorization_endpoint\":\"https://issuer.example.test/authorize\",\"token_endpoint\":\"https://issuer.example.test/token\",\"jwks_uri\":\"https://issuer.example.test/keys\"}}";
        var result = await CreateValidator(
                new StubHandler(_ => new HttpResponseMessage(HttpStatusCode.OK) { Content = JsonContent(json) }),
                IPAddress.Parse("1.2.3.4"))
            .ValidateAsync(FederationProviderKind.Generic, "https://issuer.example.test", CancellationToken.None);

        Assert.False(result.Succeeded);
        Assert.Equal("discovery_issuer_mismatch", result.Code);
    }

    [Fact]
    public async Task RejectsIssuerWithQueryAsInvalid()
    {
        var result = await CreateValidator(
                new StubHandler(_ => new HttpResponseMessage(HttpStatusCode.OK)
                {
                    Content = JsonContent("{\"issuer\":\"https://issuer.example.test?x=1\"}")
                }),
                IPAddress.Parse("1.2.3.4"))
            .ValidateAsync(FederationProviderKind.Generic, "https://issuer.example.test", CancellationToken.None);

        Assert.False(result.Succeeded);
        Assert.Equal("discovery_issuer_invalid", result.Code);
    }

    [Fact]
    public void DiscoveryHandlerDoesNotFollowRedirectsOrCarryCredentialsOrCookies()
    {
        using var handler = new FederationDiscoveryHttpMessageHandler();

        Assert.False(handler.AllowAutoRedirect);
        Assert.False(handler.UseCookies);
        Assert.False(handler.UseDefaultCredentials);
        Assert.Null(handler.Credentials);
        Assert.False(handler.UseProxy);
    }

    private static OidcFederationDiscoveryValidator CreateValidator(StubHandler handler, IPAddress address) =>
        new(new StubHttpClientFactory(handler), new StubHostAddressResolver(address));

    private static StringContent JsonContent(string value)
    {
        var content = new StringContent(value);
        content.Headers.ContentType = new MediaTypeHeaderValue("application/json");
        return content;
    }

    private sealed class StubHttpClientFactory(StubHandler handler) : IHttpClientFactory
    {
        public HttpClient CreateClient(string name) => new(handler, disposeHandler: false);
    }

    private sealed class StubHostAddressResolver(IPAddress address) : IFederationHostAddressResolver
    {
        public Task<IPAddress[]> ResolveAsync(string host, CancellationToken cancellationToken) =>
            Task.FromResult(new[] { address });
    }

    private sealed class StubHandler(Func<HttpRequestMessage, HttpResponseMessage> callback) : HttpMessageHandler
    {
        public Uri? RequestedUri { get; private set; }

        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            RequestedUri = request.RequestUri;
            return Task.FromResult(callback(request));
        }
    }
}