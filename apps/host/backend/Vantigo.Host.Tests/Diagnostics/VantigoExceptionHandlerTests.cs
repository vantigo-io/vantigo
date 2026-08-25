using System.Diagnostics;
using System.Text;
using System.Text.Json;

using Microsoft.AspNetCore.Diagnostics;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Host.Diagnostics;

namespace Vantigo.Host.Tests.Diagnostics;

public sealed class VantigoExceptionHandlerTests
{
    [Fact]
    public async Task Unhandled_exceptions_become_sanitized_problem_details()
    {
        using ServiceProvider provider = BuildProvider();
        IExceptionHandler handler = provider.GetServices<IExceptionHandler>().Single();
        HttpContext context = CreateContext(provider);

        bool handled = await handler.TryHandleAsync(
            context,
            new InvalidOperationException("Host=db;Password=super-secret"),
            CancellationToken.None);

        Assert.True(handled);
        Assert.Equal(StatusCodes.Status500InternalServerError, context.Response.StatusCode);
        Assert.Equal("application/problem+json", context.Response.ContentType);

        string body = ReadBody(context);
        Assert.DoesNotContain("super-secret", body, StringComparison.Ordinal);
        Assert.DoesNotContain("InvalidOperationException", body, StringComparison.Ordinal);

        JsonElement problem = JsonDocument.Parse(body).RootElement;
        Assert.Equal(StatusCodes.Status500InternalServerError, problem.GetProperty("status").GetInt32());
        Assert.False(string.IsNullOrWhiteSpace(problem.GetProperty("title").GetString()));
        Assert.False(string.IsNullOrWhiteSpace(problem.GetProperty("detail").GetString()));
    }

    [Fact]
    public async Task Unique_violations_from_savechanges_become_conflict_problem_details()
    {
        using ServiceProvider provider = BuildProvider();
        IExceptionHandler handler = provider.GetServices<IExceptionHandler>().Single();
        HttpContext context = CreateContext(provider);

        var exception = new Microsoft.EntityFrameworkCore.DbUpdateException(
            "Save failed.",
            new Npgsql.PostgresException("duplicate key value violates unique constraint", "ERROR", "ERROR", "23505"));
        bool handled = await handler.TryHandleAsync(context, exception, CancellationToken.None);

        Assert.True(handled);
        Assert.Equal(StatusCodes.Status409Conflict, context.Response.StatusCode);
        string body = ReadBody(context);
        Assert.DoesNotContain("duplicate key", body, StringComparison.Ordinal);
        JsonElement problem = JsonDocument.Parse(body).RootElement;
        Assert.Equal(StatusCodes.Status409Conflict, problem.GetProperty("status").GetInt32());
    }

    [Fact]
    public async Task Exclusion_violations_from_raw_sql_become_conflict_problem_details()
    {
        using ServiceProvider provider = BuildProvider();
        IExceptionHandler handler = provider.GetServices<IExceptionHandler>().Single();
        HttpContext context = CreateContext(provider);

        var exception = new Npgsql.PostgresException(
            "conflicting key value violates exclusion constraint", "ERROR", "ERROR", "23P01");
        bool handled = await handler.TryHandleAsync(context, exception, CancellationToken.None);

        Assert.True(handled);
        Assert.Equal(StatusCodes.Status409Conflict, context.Response.StatusCode);
        Assert.DoesNotContain("exclusion constraint", ReadBody(context), StringComparison.Ordinal);
    }

    [Fact]
    public async Task Problem_details_carry_the_current_activity_trace_id()
    {
        using ServiceProvider provider = BuildProvider();
        IExceptionHandler handler = provider.GetServices<IExceptionHandler>().Single();
        HttpContext context = CreateContext(provider);

        using Activity activity = new("request");
        activity.SetIdFormat(ActivityIdFormat.W3C);
        activity.Start();

        await handler.TryHandleAsync(context, new InvalidOperationException("boom"), CancellationToken.None);

        JsonElement problem = JsonDocument.Parse(ReadBody(context)).RootElement;
        Assert.Equal(activity.TraceId.ToString(), problem.GetProperty("traceId").GetString());
    }

    [Fact]
    public async Task Problem_details_fall_back_to_the_request_identifier_without_an_activity()
    {
        Activity.Current = null;
        using ServiceProvider provider = BuildProvider();
        IExceptionHandler handler = provider.GetServices<IExceptionHandler>().Single();
        HttpContext context = CreateContext(provider);
        context.TraceIdentifier = "0HN000000000A:00000001";

        await handler.TryHandleAsync(context, new InvalidOperationException("boom"), CancellationToken.None);

        JsonElement problem = JsonDocument.Parse(ReadBody(context)).RootElement;
        Assert.Equal("0HN000000000A:00000001", problem.GetProperty("traceId").GetString());
    }

    [Fact]
    public async Task Malformed_requests_are_reported_with_their_own_status_code()
    {
        using ServiceProvider provider = BuildProvider();
        IExceptionHandler handler = provider.GetServices<IExceptionHandler>().Single();
        HttpContext context = CreateContext(provider);

        bool handled = await handler.TryHandleAsync(
            context,
            new BadHttpRequestException("Unexpected end of request content.", StatusCodes.Status400BadRequest),
            CancellationToken.None);

        Assert.True(handled);
        Assert.Equal(StatusCodes.Status400BadRequest, context.Response.StatusCode);

        JsonElement problem = JsonDocument.Parse(ReadBody(context)).RootElement;
        Assert.Equal(StatusCodes.Status400BadRequest, problem.GetProperty("status").GetInt32());
        Assert.DoesNotContain("Unexpected end of request content", ReadBody(context), StringComparison.Ordinal);
    }

    [Fact]
    public async Task Aborted_requests_are_swallowed_without_a_response_body()
    {
        using ServiceProvider provider = BuildProvider();
        IExceptionHandler handler = provider.GetServices<IExceptionHandler>().Single();
        using CancellationTokenSource aborted = new();
        HttpContext context = CreateContext(provider);
        context.RequestAborted = aborted.Token;
        await aborted.CancelAsync();

        bool handled = await handler.TryHandleAsync(
            context,
            new OperationCanceledException(aborted.Token),
            aborted.Token);

        Assert.True(handled);
        Assert.Equal(StatusCodes.Status200OK, context.Response.StatusCode);
        Assert.Equal(string.Empty, ReadBody(context));
    }

    private static ServiceProvider BuildProvider()
    {
        ServiceCollection services = new();
        services.AddLogging();
        services.AddVantigoExceptionHandling();
        return services.BuildServiceProvider();
    }

    private static HttpContext CreateContext(IServiceProvider provider) => new DefaultHttpContext
    {
        RequestServices = provider,
        Request = { Method = "GET", Path = "/api/v1/customers" },
        Response = { Body = new MemoryStream() },
    };

    private static string ReadBody(HttpContext context)
    {
        context.Response.Body.Position = 0;
        using StreamReader reader = new(context.Response.Body, Encoding.UTF8, leaveOpen: true);
        return reader.ReadToEnd();
    }
}