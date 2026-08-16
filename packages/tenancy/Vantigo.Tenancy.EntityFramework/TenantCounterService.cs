using System.Data;

using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Storage;

using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Tenancy.EntityFramework;

/// <summary>
/// Allocates counter values with a PostgreSQL upsert.
/// </summary>
public sealed class TenantCounterService(ITenantContext tenantContext) : ITenantCounterService
{
    /// <inheritdoc />
    public async Task<long> NextAsync(
        DbContext db,
        string counterName,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(db);
        if (string.IsNullOrWhiteSpace(counterName))
            throw new ArgumentException("A counter name is required.", nameof(counterName));

        var tenantId = tenantContext.Current.Value;
        var connection = db.Database.GetDbConnection();
        var openedConnection = connection.State != ConnectionState.Open;

        if (openedConnection)
            await connection.OpenAsync(cancellationToken);

        try
        {
            await using var command = connection.CreateCommand();
            command.Transaction = db.Database.CurrentTransaction?.GetDbTransaction();

            var schema = db.Model.GetDefaultSchema();
            var table = string.IsNullOrWhiteSpace(schema)
                ? TenantSqlIdentifiers.QuoteIdentifier("tenant_counters")
                : TenantSqlIdentifiers.QuoteQualifiedTable(schema, "tenant_counters");

            command.CommandText = $"""
                INSERT INTO {table} (tenant_id, counter_name, next_value)
                VALUES (@tenant_id, @counter_name, 1)
                ON CONFLICT (tenant_id, counter_name)
                DO UPDATE SET next_value = tenant_counters.next_value + 1
                RETURNING next_value;
                """;

            var tenantParameter = command.CreateParameter();
            tenantParameter.ParameterName = "tenant_id";
            tenantParameter.Value = tenantId;
            command.Parameters.Add(tenantParameter);

            var nameParameter = command.CreateParameter();
            nameParameter.ParameterName = "counter_name";
            nameParameter.Value = counterName;
            command.Parameters.Add(nameParameter);

            var value = await command.ExecuteScalarAsync(cancellationToken);
            return Convert.ToInt64(value, System.Globalization.CultureInfo.InvariantCulture);
        }
        finally
        {
            if (openedConnection)
                await connection.CloseAsync();
        }
    }
}