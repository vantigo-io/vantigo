using System.Net.Http;

namespace Vantigo.Host;

/// <summary>
/// Out-of-process readiness probe for the <c>healthcheck</c> command. The
/// published container is a chiseled, distroless image with no shell, curl,
/// or wget, so Docker Compose's <c>HEALTHCHECK</c> cannot run a CMD-SHELL
/// script; it execs this application with <c>healthcheck</c> instead. A
/// single GET against the already-running instance's own
/// <c>/health/ready</c> endpoint decides the exit code: 0 for a 2xx response,
/// 1 for anything else (including a failed connection).
/// </summary>
public static class VantigoHealthCheckClient
{
    public static async Task<bool> RunAsync(CancellationToken cancellationToken = default)
    {
        var port = (Environment.GetEnvironmentVariable("ASPNETCORE_HTTP_PORTS") ?? "8080").Split(';')[0];
        using var client = new HttpClient { Timeout = TimeSpan.FromSeconds(5) };
        try
        {
            using var response = await client.GetAsync($"http://127.0.0.1:{port}/health/ready", cancellationToken);
            return response.IsSuccessStatusCode;
        }
        catch (Exception exception) when (exception is HttpRequestException or TaskCanceledException)
        {
            return false;
        }
    }
}