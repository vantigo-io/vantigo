using Npgsql;

namespace Vantigo.Communications.Services;

/// <summary>
/// A non-blocking, installation-wide lease built on a PostgreSQL advisory
/// lock, so periodic maintenance work runs on exactly one replica per cycle
/// instead of every replica contending over the same rows.
/// </summary>
internal static class CommunicationsAdvisoryLease
{
    /// <summary>Lease key for retention cleanup ("COMMRET1" as a 64-bit value).</summary>
    internal const long RetentionKey = 0x434F4D4D52455431;

    /// <summary>
    /// Runs <paramref name="action"/> if the lease is free and reports whether
    /// it ran. The advisory lock is session-scoped, so the dedicated connection
    /// stays open for the duration; another holder makes this a cheap no-op.
    /// </summary>
    internal static async Task<bool> TryRunAsync(
        NpgsqlDataSource dataSource,
        long key,
        Func<Task> action,
        CancellationToken cancellationToken)
    {
        var connection = await dataSource.OpenConnectionAsync(cancellationToken).ConfigureAwait(false);
        await using (connection.ConfigureAwait(false))
        {
            var acquire = new NpgsqlCommand("SELECT pg_try_advisory_lock(@key);", connection);
            await using (acquire.ConfigureAwait(false))
            {
                acquire.Parameters.AddWithValue("key", key);
                if (await acquire.ExecuteScalarAsync(cancellationToken).ConfigureAwait(false) is not true)
                {
                    return false;
                }
            }

            try
            {
                await action().ConfigureAwait(false);
            }
            finally
            {
                var release = new NpgsqlCommand("SELECT pg_advisory_unlock(@key);", connection);
                await using (release.ConfigureAwait(false))
                {
                    release.Parameters.AddWithValue("key", key);
                    await release.ExecuteNonQueryAsync(CancellationToken.None).ConfigureAwait(false);
                }
            }

            return true;
        }
    }
}