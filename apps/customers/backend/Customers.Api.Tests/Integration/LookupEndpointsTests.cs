using System.Net;
using System.Net.Http.Json;

namespace Vantigo.Customers.Api.Tests.Integration;

[Collection(CustomersApiCollection.Name)]
public sealed class LookupEndpointsTests : IDisposable
{
    private readonly HttpClient _client;
    private readonly CustomersApiFactory _factory;

    public LookupEndpointsTests(CustomersApiFactory factory)
    {
        _factory = factory;
        _client = factory.CreateAuthenticatedClient();
    }

    public void Dispose()
    {
        // Restore the default canned responses for the next test class.
        _factory.BrregHandler.OnRequest = null;
    }

    [Fact]
    public async Task BrregLookup_BySearch_ReturnsMappedEntities()
    {
        var response = await _client.GetFromJsonAsync<LookupResponse>("/api/v1/lookup/brreg?search=equinor");

        Assert.Equal(2, response.Data.Count);
        Assert.Equal("923609016", response.Data[0].LegalId);
        Assert.Equal("EQUINOR ASA", response.Data[0].LegalName);
        Assert.Equal("914778271", response.Data[1].LegalId);
        Assert.Equal("EQUINOR ENERGY AS", response.Data[1].LegalName);
    }

    [Fact]
    public async Task BrregLookup_ByLegalId_ReturnsExactMatch()
    {
        var response = await _client.GetFromJsonAsync<LookupResponse>("/api/v1/lookup/brreg?legalId=923609016");

        var entity = Assert.Single(response.Data);
        Assert.Equal("923609016", entity.LegalId);
        Assert.Equal("STUB ENTITY AS", entity.LegalName);
    }

    [Fact]
    public async Task BrregLookup_WhenRegistryReturnsNoMatches_ReturnsEmptyList()
    {
        _factory.BrregHandler.OnRequest = _ => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = JsonContent.Create(new { }),
        };

        var response = await _client.GetFromJsonAsync<LookupResponse>("/api/v1/lookup/brreg?search=no-such-entity");

        Assert.Empty(response.Data);
    }

    [Theory]
    [InlineData("")]
    [InlineData("search=")]
    [InlineData("search=a")]
    [InlineData("legalId=%20")]
    public async Task BrregLookup_WithInvalidQuery_ReturnsBadRequest(string queryString)
    {
        var response = await _client.GetAsync($"/api/v1/lookup/brreg?{queryString}");

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
    }

    [Fact]
    public async Task BrregLookup_WhenRegistryIsUnavailable_ReturnsBadGateway()
    {
        _factory.BrregHandler.OnRequest = _ => throw new HttpRequestException("boom");

        var response = await _client.GetAsync("/api/v1/lookup/brreg?search=equinor");

        Assert.Equal(HttpStatusCode.BadGateway, response.StatusCode);
    }

    private readonly record struct LookupResponse(List<LookupResult> Data);

    private readonly record struct LookupResult(string LegalId, string LegalName);
}