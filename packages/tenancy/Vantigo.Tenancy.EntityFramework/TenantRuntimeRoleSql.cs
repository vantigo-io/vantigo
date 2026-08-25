using Npgsql;

namespace Vantigo.Tenancy.EntityFramework;

/// <summary>
/// Builds the SQL that provisions the least-privilege runtime role: LOGIN only,
/// no superuser, no BYPASSRLS, table DML but no ownership, so the tenant RLS
/// policies actually apply to it. The Docker Compose init script and the
/// integration-test factories create the same role shape from this definition.
/// </summary>
public static class TenantRuntimeRoleSql
{
    /// <summary>
    /// Returns the statements creating <paramref name="roleName"/> and attaching
    /// default privileges to objects <paramref name="ownerRole"/> creates later
    /// (schemas, tables, sequences). Run it as a superuser or the owner role
    /// before the first migration so every migrated object is covered.
    /// </summary>
    public static string BuildCreateStatements(
        string roleName,
        string password,
        string ownerRole,
        string database)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(roleName);
        ArgumentException.ThrowIfNullOrWhiteSpace(password);
        ArgumentException.ThrowIfNullOrWhiteSpace(ownerRole);
        ArgumentException.ThrowIfNullOrWhiteSpace(database);

        var quotedRole = TenantSqlIdentifiers.QuoteIdentifier(roleName);
        var quotedOwner = TenantSqlIdentifiers.QuoteIdentifier(ownerRole);
        var quotedDatabase = TenantSqlIdentifiers.QuoteIdentifier(database);
        var quotedPassword = password.Replace("'", "''", StringComparison.Ordinal);

        return $"""
            CREATE ROLE {quotedRole}
                LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS NOINHERIT
                PASSWORD '{quotedPassword}';

            GRANT CONNECT ON DATABASE {quotedDatabase} TO {quotedRole};

            ALTER DEFAULT PRIVILEGES FOR ROLE {quotedOwner}
                GRANT USAGE ON SCHEMAS TO {quotedRole};
            ALTER DEFAULT PRIVILEGES FOR ROLE {quotedOwner}
                GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO {quotedRole};
            ALTER DEFAULT PRIVILEGES FOR ROLE {quotedOwner}
                GRANT USAGE, SELECT ON SEQUENCES TO {quotedRole};
            """;
    }

    /// <summary>
    /// Provisions the runtime role on the database the owner connection string
    /// points at, and returns a connection string for the new role. Must run
    /// before the first migration so default privileges cover every migrated
    /// object.
    /// </summary>
    public static async Task<string> ProvisionAsync(
        string ownerConnectionString,
        string roleName = "vantigo_app",
        string? password = null,
        CancellationToken cancellationToken = default)
    {
        var builder = new NpgsqlConnectionStringBuilder(ownerConnectionString);
        var ownerRole = builder.Username
            ?? throw new ArgumentException("The owner connection string must name a user.", nameof(ownerConnectionString));
        var database = builder.Database
            ?? throw new ArgumentException("The owner connection string must name a database.", nameof(ownerConnectionString));
        password ??= Guid.NewGuid().ToString("N");

        var connection = new NpgsqlConnection(ownerConnectionString);
        await using (connection.ConfigureAwait(false))
        {
            await connection.OpenAsync(cancellationToken).ConfigureAwait(false);
            var command = connection.CreateCommand();
            await using (command.ConfigureAwait(false))
            {
                command.CommandText = BuildCreateStatements(roleName, password, ownerRole, database);
                await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
            }
        }

        builder.Username = roleName;
        builder.Password = password;
        return builder.ConnectionString;
    }
}