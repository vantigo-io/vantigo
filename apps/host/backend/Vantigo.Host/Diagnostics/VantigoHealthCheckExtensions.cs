using Microsoft.AspNetCore.Diagnostics.HealthChecks;
using Microsoft.AspNetCore.Routing;

using Vantigo.Contracts.Web;
using Vantigo.Storage.Diagnostics;
using Vantigo.Tenancy;

namespace Vantigo.Host.Diagnostics;

/// <summary>
/// Wires the two container-probe endpoints Azure Container Apps (and the
/// compose healthcheck) target:
/// <list type="bullet">
/// <item><c>/health/live</c> runs no registered checks (<see cref="HealthCheckOptions.Predicate"/>
/// always false) and reports 200 whenever the process can accept a request.
/// It must never depend on PostgreSQL, storage, or any other external system.</item>
/// <item><c>/health/ready</c> runs every check tagged "ready" (PostgreSQL,
/// object storage) and reports 200 when all are healthy or degraded, 503 when
/// any is unhealthy. SMTP and OIDC are deliberately untagged and never
/// participate in either probe.</item>
/// </list>
/// Both endpoints are anonymous, skip antiforgery (also implied by being
/// GET-only) and tenant resolution, and are not subject to any rate limiter
/// policy.
/// </summary>
internal static class VantigoHealthCheckExtensions
{
    private const string ReadyTag = "ready";

    internal static IServiceCollection AddVantigoHealthChecks(this IServiceCollection services) =>
        services.AddHealthChecks()
            .AddCheck<PostgreSqlHealthCheck>("postgresql", tags: [ReadyTag])
            .AddVantigoObjectStorageHealthCheck()
            .Services;

    internal static IEndpointRouteBuilder MapVantigoHealthChecks(this IEndpointRouteBuilder endpoints)
    {
        endpoints.MapHealthChecks("/health/live", new HealthCheckOptions { Predicate = _ => false })
            .AllowAnonymous()
            .WithMetadata(new SkipTenantResolutionAttribute(), new SkipAntiforgeryAttribute())
            .DisableRateLimiting();

        endpoints.MapHealthChecks("/health/ready", new HealthCheckOptions { Predicate = registration => registration.Tags.Contains(ReadyTag) })
            .AllowAnonymous()
            .WithMetadata(new SkipTenantResolutionAttribute(), new SkipAntiforgeryAttribute())
            .DisableRateLimiting();

        return endpoints;
    }
}