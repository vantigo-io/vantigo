using System.Security.Claims;

using Microsoft.AspNetCore.Antiforgery;

using Microsoft.EntityFrameworkCore;

namespace Vantigo.Communications.Endpoints;

internal static class CommunicationEndpointHelpers
{
    internal static string CommunicationPath(HttpContext http, string suffix)
    {
        var path = (http.Request.PathBase + http.Request.Path).Value ?? string.Empty;

        const string module = "/communications";
        var moduleIndex = path.IndexOf(module, StringComparison.Ordinal);
        if (moduleIndex < 0) return "/api/v1/communications" + suffix;
        return path[..(moduleIndex + module.Length)] + suffix;
    }

    internal static Guid? CurrentUserId(HttpContext http) => Guid.TryParse(http.User.FindFirstValue(ClaimTypes.NameIdentifier), out var id) ? id : null;

    internal static async Task<IResult?> ValidateAntiforgery(HttpContext context, IAntiforgery antiforgery)
    {
        try { await antiforgery.ValidateRequestAsync(context); return null; }
        catch (AntiforgeryValidationException) { return Error(StatusCodes.Status400BadRequest, "csrf_validation_failed", "A valid X-XSRF-TOKEN header and antiforgery cookie are required."); }
    }

    internal static IResult ValidationError(Dictionary<string, string[]> errors) => Error(StatusCodes.Status400BadRequest, "invalid_request", "The request is invalid.", errors);

    internal static IResult Error(int status, string code, string message, IReadOnlyDictionary<string, string[]>? fields = null) =>
        TypedResults.Json(new CommunicationErrorResponse(new CommunicationError(code, message, fields)), statusCode: status);

    internal static bool IsUniqueViolation(DbUpdateException exception) => exception.InnerException is Npgsql.PostgresException { SqlState: Npgsql.PostgresErrorCodes.UniqueViolation };
}