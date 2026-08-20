using Microsoft.Extensions.Diagnostics.HealthChecks;

using Npgsql;

namespace Vantigo.Host.Diagnostics;

/// <summary>
/// Confirms the shared <see cref="NpgsqlDataSource"/> can open a connection and
/// execute a trivial query. Tagged "ready" so it participates in
/// <c>/health/ready</c> but never in the dependency-free <c>/health/live</c>
/// probe. An exception here is reported unhealthy by the health check
/// infrastructure itself; nothing is caught locally.
/// </summary>
internal sealed class PostgreSqlHealthCheck(NpgsqlDataSource dataSource) : IHealthCheck
{
    public async Task<HealthCheckResult> CheckHealthAsync(
        HealthCheckContext context,
        CancellationToken cancellationToken = default)
    {
        await using var connection = await dataSource.OpenConnectionAsync(cancellationToken);
        await using var command = connection.CreateCommand();
        command.CommandText = "SELECT 1";
        await command.ExecuteScalarAsync(cancellationToken);
        return HealthCheckResult.Healthy();
    }
}