using System.Text;
using System.Text.Json;

using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Testing;

/// <summary>
/// Records every /api request/response exchange the host serves during an
/// integration test run, one JSON line per exchange, into
/// $VANTIGO_CONTRACT_RECORD/&lt;process-id&gt;.jsonl. The Go port validates the
/// recordings against its OpenAPI contract. Linked into the .NET test projects
/// only; removed with the .NET host at the cutover.
/// </summary>
public static class ContractRecording
{
    public const string EnvironmentVariable = "VANTIGO_CONTRACT_RECORD";
    private const int MaxBodyBytes = 256 * 1024;
    private static readonly Lock WriteLock = new();

    public static IServiceCollection AddContractRecording(this IServiceCollection services)
    {
        if (!string.IsNullOrEmpty(Environment.GetEnvironmentVariable(EnvironmentVariable)))
        {
            services.AddTransient<IStartupFilter, RecordingStartupFilter>();
        }
        return services;
    }

    private sealed class RecordingStartupFilter : IStartupFilter
    {
        public Action<IApplicationBuilder> Configure(Action<IApplicationBuilder> next) => app =>
        {
            app.Use(RecordAsync);
            next(app);
        };
    }

    private static async Task RecordAsync(HttpContext context, RequestDelegate next)
    {
        var directory = Environment.GetEnvironmentVariable(EnvironmentVariable);
        if (string.IsNullOrEmpty(directory) || !context.Request.Path.StartsWithSegments("/api"))
        {
            await next(context);
            return;
        }

        context.Request.EnableBuffering();
        var requestBody = await ReadTextAsync(context.Request.Body, context.Request.ContentType);
        context.Request.Body.Position = 0;

        var originalBody = context.Response.Body;
        await using var captured = new MemoryStream();
        context.Response.Body = captured;
        try
        {
            await next(context);
        }
        finally
        {
            context.Response.Body = originalBody;
            captured.Position = 0;
            var responseBody = await ReadTextAsync(captured, context.Response.ContentType);
            captured.Position = 0;
            await captured.CopyToAsync(originalBody);

            var line = JsonSerializer.Serialize(new
            {
                method = context.Request.Method,
                path = context.Request.PathBase.Add(context.Request.Path).Value,
                query = context.Request.QueryString.Value ?? string.Empty,
                requestContentType = context.Request.ContentType,
                requestBody,
                status = context.Response.StatusCode,
                responseContentType = context.Response.ContentType,
                responseBody,
            });
            Directory.CreateDirectory(directory);
            var file = Path.Combine(directory, $"{Environment.ProcessId}.jsonl");
            lock (WriteLock)
            {
                File.AppendAllText(file, line + "\n");
            }
        }
    }

    private static async Task<string?> ReadTextAsync(Stream body, string? contentType)
    {
        // A cheap pre-check for streams that are already seekable and already know
        // their full length (e.g. the captured in-memory response). The request
        // body, once EnableBuffering() has run, is a FileBufferingReadStream whose
        // Length is only what has been buffered so far (0 before the first read),
        // so it never trips this pre-check for a body that turns out to be large;
        // the post-read Encoding.UTF8.GetByteCount check below is what actually
        // enforces the cap in that case.
        if (!IsText(contentType) || (body.CanSeek && body.Length > MaxBodyBytes))
        {
            return null;
        }
        using var reader = new StreamReader(body, Encoding.UTF8, detectEncodingFromByteOrderMarks: false, leaveOpen: true);
        var text = await reader.ReadToEndAsync();
        if (text.Length == 0)
        {
            return null;
        }
        if (Encoding.UTF8.GetByteCount(text) > MaxBodyBytes)
        {
            return null;
        }
        return text;
    }

    private static bool IsText(string? contentType) =>
        contentType is not null &&
        (contentType.Contains("json", StringComparison.OrdinalIgnoreCase) ||
         contentType.StartsWith("text/", StringComparison.OrdinalIgnoreCase) ||
         contentType.Contains("x-www-form-urlencoded", StringComparison.OrdinalIgnoreCase));
}