using Microsoft.AspNetCore.Diagnostics;
using Microsoft.AspNetCore.Mvc;
using Microsoft.EntityFrameworkCore;

using Npgsql;

namespace Vantigo.Host.Diagnostics;

/// <summary>
/// Translates unhandled exceptions into sanitized RFC 7807 Problem Details.
/// Nothing from the exception reaches the caller: the response carries only a
/// generic detail plus the trace id of the current activity, so a failure a user
/// reports can be correlated with the exception logged here.
/// </summary>
internal sealed class VantigoExceptionHandler(
    IProblemDetailsService problemDetailsService,
    ILogger<VantigoExceptionHandler> logger) : IExceptionHandler
{
    internal const string UnexpectedErrorDetail =
        "The request could not be completed. Quote the trace id when reporting this problem.";
    internal const string MalformedRequestDetail = "The request could not be read.";
    internal const string ConflictDetail =
        "The request conflicts with data that already exists. Verify the values and try again.";

    public async ValueTask<bool> TryHandleAsync(
        HttpContext httpContext,
        Exception exception,
        CancellationToken cancellationToken)
    {
        if (httpContext.RequestAborted.IsCancellationRequested)
        {
            // The caller is already gone: there is no response to write and no
            // incident to raise, so this must not be reported as a server error.
            logger.LogDebug(
                exception,
                "{Method} {Path} was aborted by the caller.",
                httpContext.Request.Method,
                httpContext.Request.Path);
            return true;
        }

        (int statusCode, string detail) = Map(exception);
        string traceId = VantigoExceptionHandlingExtensions.TraceId(httpContext);

        if (statusCode >= StatusCodes.Status500InternalServerError)
        {
            logger.LogError(
                exception,
                "Unhandled exception while processing {Method} {Path} (trace {TraceId}).",
                httpContext.Request.Method,
                httpContext.Request.Path,
                traceId);
        }
        else
        {
            logger.LogWarning(
                exception,
                "Rejected {Method} {Path} with status {StatusCode} (trace {TraceId}).",
                httpContext.Request.Method,
                httpContext.Request.Path,
                statusCode,
                traceId);
        }

        httpContext.Response.StatusCode = statusCode;
        return await problemDetailsService.TryWriteAsync(new ProblemDetailsContext
        {
            HttpContext = httpContext,
            Exception = exception,
            ProblemDetails = new ProblemDetails
            {
                Status = statusCode,
                Detail = detail,
            },
        });
    }

    /// <summary>
    /// Maps an exception type to the status code and the sanitized detail the
    /// caller is allowed to see. Add one arm per typed domain exception; the
    /// title is left to the RFC 7807 defaults for the resulting status code.
    /// </summary>
    private static (int StatusCode, string Detail) Map(Exception exception) => exception switch
    {
        BadHttpRequestException badRequest => (badRequest.StatusCode, MalformedRequestDetail),
        // Unique and exclusion violations are the database backstop behind the
        // endpoints' friendly pre-checks: two concurrent requests can both pass
        // the pre-check, and the loser's constraint violation is an expected
        // conflict, not a server fault.
        DbUpdateException { InnerException: PostgresException inner } when IsConstraintConflict(inner) =>
            (StatusCodes.Status409Conflict, ConflictDetail),
        PostgresException postgres when IsConstraintConflict(postgres) =>
            (StatusCodes.Status409Conflict, ConflictDetail),
        _ => (StatusCodes.Status500InternalServerError, UnexpectedErrorDetail),
    };

    private static bool IsConstraintConflict(PostgresException exception) =>
        exception.SqlState is PostgresErrorCodes.UniqueViolation or PostgresErrorCodes.ExclusionViolation;
}