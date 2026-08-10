using System.Text.Json.Serialization;

using Microsoft.AspNetCore.Http.HttpResults;

namespace Vantigo.Customers.Endpoints.Lookup;

/// <summary>
/// Looks up business entities in Brønnøysundregisteret (the Norwegian central register
/// of legal entities) through its open Enhetsregisteret API. Supports free-text search
/// by name and exact lookup by organisation number, so frontends can both suggest
/// entities while typing and refresh already-selected entities.
/// </summary>
internal static class BrregLookupEndpoint
{
    private const int MinSearchLength = 2;
    private const int MaxResults = 10;

    internal const string HttpClientName = "brreg";

    internal static async Task<Results<Ok<Response>, ProblemHttpResult>> Handler(
        [AsParameters] Request request,
        IHttpClientFactory httpClientFactory,
        ILoggerFactory loggerFactory,
        CancellationToken cancellationToken)
    {
        if (Validate(request) is { } problem)
        {
            return problem;
        }

        var query = request.LegalId is { } legalId
            ? $"organisasjonsnummer={Uri.EscapeDataString(legalId.Trim())}"
            : $"navn={Uri.EscapeDataString(request.Search!.Trim())}";

        var client = httpClientFactory.CreateClient(HttpClientName);

        try
        {
            var result = await client.GetFromJsonAsync<BrregSearchResult>(
                $"/enhetsregisteret/api/enheter?{query}&size={MaxResults}",
                cancellationToken);

            var entities = result.Embedded?.Entities ?? [];

            return TypedResults.Ok(new Response
            {
                Data = entities
                    .Select(entity => new LookupResult
                    {
                        LegalId = entity.OrganisationNumber,
                        LegalName = entity.Name,
                    })
                    .ToList(),
            });
        }
        catch (Exception exception) when (exception is HttpRequestException or TaskCanceledException && !cancellationToken.IsCancellationRequested)
        {
            loggerFactory
                .CreateLogger(nameof(BrregLookupEndpoint))
                .LogWarning(exception, "Brreg lookup failed for query {Query}", query);

            return TypedResults.Problem(
                title: "Lookup service unavailable",
                detail: "The Brønnøysundregisteret lookup service could not be reached. Please try again later.",
                statusCode: StatusCodes.Status502BadGateway);
        }
    }

    private static ProblemHttpResult? Validate(Request request)
    {
        if (request.LegalId is { } legalId && !string.IsNullOrWhiteSpace(legalId))
        {
            return null;
        }

        if (request.Search is { } search && search.Trim().Length >= MinSearchLength)
        {
            return null;
        }

        return TypedResults.Problem(
            title: "Invalid lookup query",
            detail: $"Either 'legalId' or a 'search' of at least {MinSearchLength} characters must be provided.",
            statusCode: StatusCodes.Status400BadRequest);
    }

    internal readonly record struct Request
    {
        /// <summary>Free-text search matching the legal name of the entity.</summary>
        public string? Search { get; init; }

        /// <summary>Exact organisation number of the entity to look up.</summary>
        public string? LegalId { get; init; }
    }

    internal readonly record struct Response
    {
        public required List<LookupResult> Data { get; init; }
    }

    internal readonly record struct LookupResult
    {
        public required string LegalId { get; init; }
        public required string LegalName { get; init; }
    }

    // The subset of the Enhetsregisteret search response that the lookup relies on,
    // see https://data.brreg.no/enhetsregisteret/api/dokumentasjon.
    private readonly record struct BrregSearchResult(
        [property: JsonPropertyName("_embedded")] BrregEmbedded? Embedded);

    private readonly record struct BrregEmbedded(
        [property: JsonPropertyName("enheter")] List<BrregEntity> Entities);

    private readonly record struct BrregEntity(
        [property: JsonPropertyName("organisasjonsnummer")] string OrganisationNumber,
        [property: JsonPropertyName("navn")] string Name);
}