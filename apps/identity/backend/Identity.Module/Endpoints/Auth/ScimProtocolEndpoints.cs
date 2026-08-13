using System.Text.Json;
using System.Threading.RateLimiting;

using Microsoft.EntityFrameworkCore;

using Vantigo.Contracts.Web;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Endpoints.Auth;

internal static class ScimProtocolEndpoints
{
    internal static void Map(IEndpointRouteBuilder app)
    {
        var scim = app.MapGroup(ScimProtocolService.BasePath)
            .WithTags("SCIM 2.0")
            .WithMetadata(new SkipAntiforgeryAttribute())
            .AddEndpointFilter<ScimIngressEndpointFilter>();

        scim.MapGet("/ServiceProviderConfig", (ScimProtocolService service, CancellationToken token) => service.ServiceProviderConfigAsync(token));
        scim.MapGet("/Schemas", (ScimProtocolService service, CancellationToken token) => service.SchemasAsync(token));
        scim.MapGet("/ResourceTypes", (ScimProtocolService service, CancellationToken token) => service.ResourceTypesAsync(token));
        scim.MapPost("/Users", async (HttpRequest request, ScimProtocolService service, CancellationToken token) => await Body(service.CreateUserAsync, request, token));
        scim.MapGet("/Users/{id}", (string id, ScimProtocolService service, CancellationToken token) => service.GetUserAsync(id, token));
        scim.MapGet("/Users", (HttpRequest request, ScimProtocolService service, CancellationToken token) => service.ListUsersAsync(request, token));
        scim.MapPut("/Users/{id}", async (string id, HttpRequest request, ScimProtocolService service, CancellationToken token) => await Body((body, ct) => service.PutUserAsync(id, body, request, ct), request, token));
        scim.MapPatch("/Users/{id}", async (string id, HttpRequest request, ScimProtocolService service, CancellationToken token) => await Body((body, ct) => service.PatchUserAsync(id, body, request, ct), request, token));
        scim.MapDelete("/Users/{id}", (string id, HttpRequest request, ScimProtocolService service, CancellationToken token) => service.DeleteUserAsync(id, request, token));
        scim.MapPost("/Groups", async (HttpRequest request, ScimProtocolService service, CancellationToken token) => await Body(service.CreateGroupAsync, request, token));
        scim.MapGet("/Groups/{id}", (string id, HttpRequest request, ScimProtocolService service, CancellationToken token) => service.GetGroupAsync(id, request, token));
        scim.MapGet("/Groups", (HttpRequest request, ScimProtocolService service, CancellationToken token) => service.ListGroupsAsync(request, token));
        scim.MapPatch("/Groups/{id}", async (string id, HttpRequest request, ScimProtocolService service, CancellationToken token) => await Body((body, ct) => service.PatchGroupAsync(id, body, request, ct), request, token));
        scim.MapDelete("/Groups/{id}", (string id, HttpRequest request, ScimProtocolService service, CancellationToken token) => service.DeleteGroupAsync(id, request, token));
    }

    private static async Task<IResult> Body(Func<JsonElement, CancellationToken, Task<IResult>> operation, HttpRequest request, CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(request.ContentType) || !request.ContentType.Split(';', 2)[0].Trim().Equals(ScimProtocolService.ScimMediaType, StringComparison.OrdinalIgnoreCase))
            return Error(StatusCodes.Status415UnsupportedMediaType, "invalidSyntax", "SCIM request bodies must use application/scim+json.");
        try
        {
            const int maxBodyBytes = ScimProtocolService.MaximumRequestBodyBytes;
            if (request.ContentLength is > maxBodyBytes)
                return Error(StatusCodes.Status413PayloadTooLarge, "tooLarge", "The SCIM request body exceeds the 256 KiB limit.");

            await using var bounded = new MemoryStream();
            var buffer = new byte[32 * 1024];
            var total = 0;
            int read;
            while ((read = await request.Body.ReadAsync(buffer, cancellationToken)) != 0)
            {
                total += read;
                if (total > maxBodyBytes)
                    return Error(StatusCodes.Status413PayloadTooLarge, "tooLarge", "The SCIM request body exceeds the 256 KiB limit.");
                await bounded.WriteAsync(buffer.AsMemory(0, read), cancellationToken);
            }

            bounded.Position = 0;
            using var document = await JsonDocument.ParseAsync(bounded, cancellationToken: cancellationToken);
            return await operation(document.RootElement.Clone(), cancellationToken);
        }
        catch (JsonException)
        {
            return Error(StatusCodes.Status400BadRequest, "invalidSyntax", "The request body is not valid JSON.");
        }
    }

    private static IResult Error(int status, string scimType, string detail) => TypedResults.Json(new
    {
        schemas = new[] { "urn:ietf:params:scim:api:messages:2.0:Error" },
        status = status.ToString(System.Globalization.CultureInfo.InvariantCulture),
        scimType,
        detail,
    }, contentType: ScimProtocolService.ScimMediaType, statusCode: status);
}

internal sealed class ScimIngressEndpointFilter(ScimProtocolService protocolService) : IEndpointFilter
{
    public async ValueTask<object?> InvokeAsync(EndpointFilterInvocationContext context, EndpointFilterDelegate next)
    {
        var rejection = await protocolService.AuthenticateAndRateLimitIngressAsync(context.HttpContext.RequestAborted);
        if (rejection is not null) return rejection;
        try
        {
            return await next(context);
        }
        catch (DbUpdateConcurrencyException)
        {
            return ScimProtocolService.ConcurrencyError();
        }
    }
}

public sealed class ScimIngressRateLimiter
{
    private readonly PartitionedRateLimiter<string> limiter = PartitionedRateLimiter.Create<string, string>(key =>
        RateLimitPartition.GetFixedWindowLimiter(key, _ => new FixedWindowRateLimiterOptions
        {
            PermitLimit = 120,
            Window = TimeSpan.FromMinutes(1),
            QueueLimit = 0,
            AutoReplenishment = true,
        }));

    public async ValueTask<bool> AllowAsync(string key, CancellationToken cancellationToken)
    {
        using var lease = await limiter.AcquireAsync(key, 1, cancellationToken);
        return lease.IsAcquired;
    }
}