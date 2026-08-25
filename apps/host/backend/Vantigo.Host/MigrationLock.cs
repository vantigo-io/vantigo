using Microsoft.Extensions.Options;

using Npgsql;

using Vantigo.Configuration;

namespace Vantigo.Host;

/// <summary>
/// Serializes migration runs across processes with a PostgreSQL advisory lock,
/// so a multi-replica rollout or a retried migration job cannot run two
/// migrators concurrently: the second waits for the first and then re-runs
/// idempotently over the already-migrated schema.
/// </summary>
internal static class MigrationLock
{
    /// <summary>
    /// Fixed installation-wide lock key ("VANTIGO1" as a 64-bit value). Every
    /// migrator of every version must use the same key.
    /// </summary>
    internal const long Key = 0x56414E5449474F31;

    internal static async Task RunAsync(IServiceProvider services, Func<Task> migrate)
    {
        var connectionStrings = services.GetRequiredService<IOptions<ConnectionStringsOptions>>().Value;
        var connectionString = connectionStrings.ResolveMigrationsOverride() ?? connectionStrings.Resolve();
        var logger = services.GetRequiredService<ILoggerFactory>().CreateLogger("Vantigo.Migrations");

        // The advisory lock is session-scoped, so this dedicated connection
        // must stay open for the whole migration run; the migrations themselves
        // execute over their own connections.
        var connection = new NpgsqlConnection(connectionString);
        await using (connection.ConfigureAwait(false))
        {
            await connection.OpenAsync().ConfigureAwait(false);

            logger.LogInformation("Acquiring the migration advisory lock.");
            var acquire = new NpgsqlCommand("SELECT pg_advisory_lock(@key);", connection);
            await using (acquire.ConfigureAwait(false))
            {
                acquire.Parameters.AddWithValue("key", Key);
                // Waiting on another migrator can legitimately take minutes;
                // never time the wait out from the client side.
                acquire.CommandTimeout = 0;
                await acquire.ExecuteNonQueryAsync().ConfigureAwait(false);
            }

            logger.LogInformation("Migration advisory lock acquired.");
            try
            {
                await migrate().ConfigureAwait(false);
            }
            finally
            {
                var release = new NpgsqlCommand("SELECT pg_advisory_unlock(@key);", connection);
                await using (release.ConfigureAwait(false))
                {
                    release.Parameters.AddWithValue("key", Key);
                    await release.ExecuteNonQueryAsync().ConfigureAwait(false);
                }

                logger.LogInformation("Migration advisory lock released.");
            }
        }
    }
}