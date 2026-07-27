using System.Net;
using System.Text;

namespace Vantigo.Customers.Api.Tests.Integration;

/// <summary>
/// A stand-in for the open Enhetsregisteret API so integration tests never call the
/// real registry. Tests can override <see cref="OnRequest"/> to simulate specific
/// responses or failures; the default returns a small canned result set for name
/// searches and an exact match for organisation number lookups.
/// </summary>
public sealed class StubBrregHandler : HttpMessageHandler
{
    /// <summary>Overrides the response per request. Reset to null to restore defaults.</summary>
    public Func<HttpRequestMessage, HttpResponseMessage>? OnRequest { get; set; }

    protected override Task<HttpResponseMessage> SendAsync(
        HttpRequestMessage request,
        CancellationToken cancellationToken)
    {
        return Task.FromResult(OnRequest?.Invoke(request) ?? DefaultResponse(request));
    }

    private static HttpResponseMessage DefaultResponse(HttpRequestMessage request)
    {
        var query = System.Web.HttpUtility.ParseQueryString(request.RequestUri!.Query);

        var json = query["organisasjonsnummer"] is { } organisationNumber
            ? $$"""
                {
                  "_embedded": {
                    "enheter": [
                      { "organisasjonsnummer": "{{organisationNumber}}", "navn": "STUB ENTITY AS" }
                    ]
                  }
                }
                """
            : """
              {
                "_embedded": {
                  "enheter": [
                    { "organisasjonsnummer": "923609016", "navn": "EQUINOR ASA" },
                    { "organisasjonsnummer": "914778271", "navn": "EQUINOR ENERGY AS" }
                  ]
                }
              }
              """;

        return new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent(json, Encoding.UTF8, "application/json"),
        };
    }
}
