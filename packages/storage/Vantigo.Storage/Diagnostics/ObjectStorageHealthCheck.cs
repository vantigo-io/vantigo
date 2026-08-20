using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Diagnostics.HealthChecks;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Storage.Initialization;

namespace Vantigo.Storage.Diagnostics;

/// <summary>
/// Verifies the configured object storage backend can reach its container (S3
/// bucket, Azure Blob container, or local root). Storage is optional: when
/// <see cref="StorageOptions.IsConfigured"/> is false the check is skipped and
/// reported healthy, since an unconfigured backend already fails closed for
/// every real operation and must not block readiness.
/// </summary>
internal sealed class ObjectStorageHealthCheck(IStorageBackend backend, IOptions<StorageOptions> options) : IHealthCheck
{
    // Never written; only its (non-)existence is probed, which round-trips the
    // provider's connectivity, credentials, and container/bucket access.
    private const string ProbeKey = "healthcheck/probe";

    public Task<HealthCheckResult> CheckHealthAsync(
        HealthCheckContext context,
        CancellationToken cancellationToken = default) =>
        options.Value.IsConfigured
            ? CheckBackendAsync(cancellationToken)
            : Task.FromResult(HealthCheckResult.Healthy("Object storage is not configured; skipping the readiness check."));

    private async Task<HealthCheckResult> CheckBackendAsync(CancellationToken cancellationToken)
    {
        await backend.ExistsAsync(ProbeKey, cancellationToken);
        return HealthCheckResult.Healthy();
    }
}

public static class ObjectStorageHealthCheckBuilderExtensions
{
    /// <summary>
    /// Registers the object storage readiness check, tagged "ready" so it
    /// participates in <c>/health/ready</c> but never in a dependency-free
    /// liveness probe.
    /// </summary>
    public static IHealthChecksBuilder AddVantigoObjectStorageHealthCheck(this IHealthChecksBuilder builder) =>
        builder.AddCheck<ObjectStorageHealthCheck>("object_storage", tags: ["ready"]);
}