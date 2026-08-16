using System.Data.Common;

using Microsoft.EntityFrameworkCore.Diagnostics;

using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Tenancy.EntityFramework;

/// <summary>
/// Sets the transaction-local PostgreSQL tenant GUC after a transaction begins.
/// An unresolved tenant deliberately leaves the GUC unset, allowing RLS to fail
/// closed.
/// </summary>
public sealed class TenantConnectionInterceptor(ITenantContext tenantContext) : DbTransactionInterceptor
{
    /// <inheritdoc />
    public override DbTransaction TransactionStarted(
        DbConnection connection,
        TransactionEndEventData eventData,
        DbTransaction result)
    {
        SetTenantGuc(connection, result);
        return result;
    }

    /// <inheritdoc />
    public override async ValueTask<DbTransaction> TransactionStartedAsync(
        DbConnection connection,
        TransactionEndEventData eventData,
        DbTransaction result,
        CancellationToken cancellationToken = default)
    {
        await SetTenantGucAsync(connection, result, cancellationToken);
        return result;
    }

    private void SetTenantGuc(DbConnection connection, DbTransaction transaction)
    {
        if (!tenantContext.IsResolved)
            return;

        using var command = connection.CreateCommand();
        command.Transaction = transaction;
        command.CommandText = "SELECT set_config('app.tenant_id', @tenant_id, true);";

        var parameter = command.CreateParameter();
        parameter.ParameterName = "tenant_id";
        parameter.Value = tenantContext.Current.Value.ToString();
        command.Parameters.Add(parameter);
        command.ExecuteNonQuery();
    }

    private async Task SetTenantGucAsync(
        DbConnection connection,
        DbTransaction transaction,
        CancellationToken cancellationToken)
    {
        if (!tenantContext.IsResolved)
            return;

        await using var command = connection.CreateCommand();
        command.Transaction = transaction;
        command.CommandText = "SELECT set_config('app.tenant_id', @tenant_id, true);";

        var parameter = command.CreateParameter();
        parameter.ParameterName = "tenant_id";
        parameter.Value = tenantContext.Current.Value.ToString();
        command.Parameters.Add(parameter);
        await command.ExecuteNonQueryAsync(cancellationToken);
    }
}