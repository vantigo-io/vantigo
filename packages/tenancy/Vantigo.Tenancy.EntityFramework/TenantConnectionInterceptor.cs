using System.Data.Common;

using Microsoft.EntityFrameworkCore.Diagnostics;

using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Tenancy.EntityFramework;

/// <summary>
/// Applies the session-scoped PostgreSQL tenant setting every time EF Core opens
/// a connection, so row-level security covers transaction-less reads as well as
/// transactional work. An unresolved tenant explicitly clears the setting: a
/// pooled physical connection can never carry a previous request's tenant, and
/// the RLS policies treat an empty setting as "match nothing", so access fails
/// closed.
/// </summary>
public sealed class TenantConnectionInterceptor(ITenantContext tenantContext) : DbConnectionInterceptor
{
    private const string ApplyTenantSettingSql = "SELECT set_config('app.tenant_id', @tenant_id, false);";

    /// <inheritdoc />
    public override void ConnectionOpened(DbConnection connection, ConnectionEndEventData eventData)
    {
        using var command = CreateApplyCommand(connection);
        command.ExecuteNonQuery();
    }

    /// <inheritdoc />
    public override async Task ConnectionOpenedAsync(
        DbConnection connection,
        ConnectionEndEventData eventData,
        CancellationToken cancellationToken = default)
    {
        var command = CreateApplyCommand(connection);
        await using (command.ConfigureAwait(false))
        {
            await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
        }
    }

    private DbCommand CreateApplyCommand(DbConnection connection)
    {
        var command = connection.CreateCommand();
        command.CommandText = ApplyTenantSettingSql;

        var parameter = command.CreateParameter();
        parameter.ParameterName = "tenant_id";
        parameter.Value = tenantContext.IsResolved
            ? tenantContext.Current.Value.ToString()
            : string.Empty;
        command.Parameters.Add(parameter);
        return command;
    }
}