using System.Data;
using System.Data.Common;

using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Storage;

using Npgsql;

using NpgsqlTypes;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Endpoints.Consumption.Dtos;

namespace Vantigo.Energy.Endpoints.Consumption;

internal static class ConsumptionAggregateQuery
{
    internal static Task<IReadOnlyList<ConsumptionAggregateResponse>> ForMeteringPointAsync(
        EnergyDbContext db,
        int meteringPointId,
        DateTimeOffset from,
        DateTimeOffset to,
        string resolution,
        string timeZone,
        CancellationToken cancellationToken) =>
        ExecuteAsync<ConsumptionAggregateResponse>(
            db,
            """
            WITH bucketed AS (
                SELECT date_trunc(@resolution, c."start" AT TIME ZONE @time_zone) AS bucket_local,
                       c.quantity_kwh,
                       c.quality
                FROM energy.consumption_intervals AS c
                WHERE c.metering_point_id = @metering_point_id
                  AND c.is_current
                  AND c."start" >= @from
                  AND c."end" <= @to
            )
            SELECT bucket_local AT TIME ZONE @time_zone AS bucket_start,
                   (bucket_local + CASE @resolution
                       WHEN 'hour' THEN interval '1 hour'
                       WHEN 'day' THEN interval '1 day'
                       WHEN 'month' THEN interval '1 month'
                   END) AT TIME ZONE @time_zone AS bucket_end,
                   SUM(quantity_kwh) AS quantity_kwh,
                   COUNT(*) AS interval_count,
                   BOOL_OR(quality IN ('Estimated', 'Corrected')) AS has_estimated
            FROM bucketed
            GROUP BY bucket_local
            ORDER BY bucket_local
            """,
            (command, parameters) =>
            {
                AddCommonParameters(parameters, from, to, resolution, timeZone);
                parameters.Add(new NpgsqlParameter("metering_point_id", NpgsqlDbType.Integer) { Value = meteringPointId });
            },
            static reader => new ConsumptionAggregateResponse(
                reader.GetFieldValue<DateTimeOffset>(0),
                reader.GetFieldValue<DateTimeOffset>(1),
                reader.GetFieldValue<decimal>(2),
                reader.GetFieldValue<long>(3),
                reader.GetBoolean(4)),
            cancellationToken);

    internal static Task<IReadOnlyList<CustomerConsumptionAggregateResponse>> ForCustomerMeteringPointAsync(
        EnergyDbContext db,
        int customerId,
        int meteringPointId,
        DateTimeOffset from,
        DateTimeOffset to,
        string resolution,
        string timeZone,
        CancellationToken cancellationToken) =>
        ExecuteAsync<CustomerConsumptionAggregateResponse>(
            db,
            """
            WITH bucketed AS (
                SELECT date_trunc(@resolution, c."start" AT TIME ZONE @time_zone) AS bucket_local,
                       c.metering_point_id,
                       c.quantity_kwh,
                       c.quality
                FROM energy.consumption_intervals AS c
                WHERE c.metering_point_id = @metering_point_id
                  AND c.is_current
                  AND c."start" >= @from
                  AND c."end" <= @to
                  AND EXISTS (
                      SELECT 1
                      FROM energy.supply_periods AS p
                      WHERE p.customer_id = @customer_id
                        AND p.metering_point_id = c.metering_point_id
                        AND p.status <> 'Cancelled'
                        AND c."start" >= p."start"
                        AND (p."end" IS NULL OR c."end" <= p."end")
                  )
            )
            SELECT metering_point_id,
                   bucket_local AT TIME ZONE @time_zone AS bucket_start,
                   (bucket_local + CASE @resolution
                       WHEN 'hour' THEN interval '1 hour'
                       WHEN 'day' THEN interval '1 day'
                       WHEN 'month' THEN interval '1 month'
                   END) AT TIME ZONE @time_zone AS bucket_end,
                   SUM(quantity_kwh) AS quantity_kwh,
                   COUNT(*) AS interval_count,
                   BOOL_OR(quality IN ('Estimated', 'Corrected')) AS has_estimated
            FROM bucketed
            GROUP BY metering_point_id, bucket_local
            ORDER BY bucket_local
            """,
            (command, parameters) =>
            {
                AddCommonParameters(parameters, from, to, resolution, timeZone);
                parameters.Add(new NpgsqlParameter("customer_id", NpgsqlDbType.Integer) { Value = customerId });
                parameters.Add(new NpgsqlParameter("metering_point_id", NpgsqlDbType.Integer) { Value = meteringPointId });
            },
            static reader => new CustomerConsumptionAggregateResponse(
                reader.GetFieldValue<int>(0),
                reader.GetFieldValue<DateTimeOffset>(1),
                reader.GetFieldValue<DateTimeOffset>(2),
                reader.GetFieldValue<decimal>(3),
                reader.GetFieldValue<long>(4),
                reader.GetBoolean(5)),
            cancellationToken);

    private static void AddCommonParameters(
        NpgsqlParameterCollection parameters,
        DateTimeOffset from,
        DateTimeOffset to,
        string resolution,
        string timeZone)
    {
        parameters.Add(new NpgsqlParameter("resolution", NpgsqlDbType.Text) { Value = resolution });
        parameters.Add(new NpgsqlParameter("time_zone", NpgsqlDbType.Text) { Value = timeZone });
        parameters.Add(new NpgsqlParameter("from", NpgsqlDbType.TimestampTz) { Value = from.ToUniversalTime() });
        parameters.Add(new NpgsqlParameter("to", NpgsqlDbType.TimestampTz) { Value = to.ToUniversalTime() });
    }

    private static async Task<IReadOnlyList<T>> ExecuteAsync<T>(
        EnergyDbContext db,
        string sql,
        Action<DbCommand, NpgsqlParameterCollection> addParameters,
        Func<DbDataReader, T> map,
        CancellationToken cancellationToken)
    {
        var connection = db.Database.GetDbConnection();
        var shouldClose = connection.State != ConnectionState.Open;
        if (shouldClose) await db.Database.OpenConnectionAsync(cancellationToken);

        try
        {
            await using var command = connection.CreateCommand();
            command.CommandText = sql;
            if (db.Database.CurrentTransaction is { } transaction)
                command.Transaction = transaction.GetDbTransaction();
            addParameters(command, (NpgsqlParameterCollection)command.Parameters);

            await using var reader = await command.ExecuteReaderAsync(cancellationToken);
            var result = new List<T>();
            while (await reader.ReadAsync(cancellationToken)) result.Add(map(reader));
            return result;
        }
        finally
        {
            if (shouldClose) await db.Database.CloseConnectionAsync();
        }
    }
}