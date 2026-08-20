using System.Net;

namespace Vantigo.Customers.Module.Tests.Integration;

/// <summary>
/// Every response the host produces carries the browser security headers, not
/// just the SPA document: an API response rendered directly in a browser tab is
/// exactly the sniffing and framing surface these headers close.
/// </summary>
[Collection(CustomersApiCollection.Name)]
public sealed class SecurityHeadersIntegrationTests
{
    private readonly HttpClient _client;

    public SecurityHeadersIntegrationTests(CustomersApiFactory factory)
    {
        _client = factory.CreateAuthenticatedClient();
    }

    public static IEnumerable<object[]> Responses() =>
    [
        // The SPA entry point. No frontend is published in the test host, so it
        // answers 404 -- the headers still have to be there.
        ["/"],
        ["/customers/42"],
        // A successful API response, and an API response that never reached an
        // endpoint at all.
        ["/api/v1/customers"],
        ["/api/v1/customers/does-not-exist"],
    ];

    [Theory]
    [MemberData(nameof(Responses))]
    public async Task EveryResponseCarriesTheSecurityHeaders(string path)
    {
        HttpResponseMessage response = await _client.GetAsync(path);

        Assert.Equal("nosniff", Single(response, "X-Content-Type-Options"));
        Assert.Equal("DENY", Single(response, "X-Frame-Options"));
        Assert.Equal("no-referrer", Single(response, "Referrer-Policy"));
        Assert.Contains("camera=()", Single(response, "Permissions-Policy"), StringComparison.Ordinal);
    }

    [Theory]
    [MemberData(nameof(Responses))]
    public async Task EveryResponseCarriesAnEnforcedContentSecurityPolicy(string path)
    {
        HttpResponseMessage response = await _client.GetAsync(path);

        string policy = Single(response, "Content-Security-Policy");

        Assert.False(response.Headers.Contains("Content-Security-Policy-Report-Only"));
        Assert.Contains("default-src 'self'", policy, StringComparison.Ordinal);
        Assert.Contains("frame-ancestors 'none'", policy, StringComparison.Ordinal);
        Assert.Contains("object-src 'none'", policy, StringComparison.Ordinal);
        Assert.Contains("base-uri 'self'", policy, StringComparison.Ordinal);
        // The runtime-configuration script the SPA document injects is allowed by
        // hash, never by 'unsafe-inline'.
        Assert.Contains("script-src 'self' 'sha256-", policy, StringComparison.Ordinal);
    }

    [Fact]
    public async Task AnUnknownHostHeader_IsRejectedBeforeReachingTheApplication()
    {
        using HttpRequestMessage request = new(HttpMethod.Get, "/api/v1/customers");
        request.Headers.Host = "vantigo.attacker.example";

        HttpResponseMessage response = await _client.SendAsync(request);

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
    }

    /// <summary>
    /// The container health probe is an in-process plain-HTTP request to
    /// http://127.0.0.1:8080/health/ready, because the chiseled image has no shell
    /// to run curl in. Host filtering runs before routing, so it cannot exempt
    /// those endpoints by metadata the way tenancy, antiforgery and rate limiting
    /// do -- the loopback Host header has to be accepted outright, port and all.
    /// A 400 here marks the container permanently unhealthy.
    /// </summary>
    [Theory]
    [InlineData("127.0.0.1:8080")]
    [InlineData("127.0.0.1")]
    [InlineData("localhost:8080")]
    [InlineData("[::1]:8080")]
    public async Task ALoopbackProbeHostHeader_ReachesTheApplication(string host)
    {
        using HttpRequestMessage request = new(HttpMethod.Get, "/api/v1/customers");
        request.Headers.Host = host;

        HttpResponseMessage response = await _client.SendAsync(request);

        Assert.NotEqual(HttpStatusCode.BadRequest, response.StatusCode);
    }

    /// <summary>
    /// Nothing in this pipeline redirects plain HTTP. A health probe treats any
    /// non-2xx as a failure, so a 307 to https would fail it just as hard as a
    /// 400. UseHsts only ever adds a response header, and only over https; this
    /// pins that no HTTPS-redirection middleware creeps in beside it.
    /// </summary>
    [Theory]
    [MemberData(nameof(Responses))]
    public async Task PlainHttpIsNeverRedirected(string path)
    {
        HttpResponseMessage response = await _client.GetAsync(path);

        Assert.NotInRange((int)response.StatusCode, 300, 399);
        Assert.False(response.Headers.Contains("Strict-Transport-Security"));
    }

    private static string Single(HttpResponseMessage response, string name) =>
        Assert.Single(response.Headers.TryGetValues(name, out IEnumerable<string>? values)
            ? values
            : throw new InvalidOperationException($"The response carried no {name} header."));
}