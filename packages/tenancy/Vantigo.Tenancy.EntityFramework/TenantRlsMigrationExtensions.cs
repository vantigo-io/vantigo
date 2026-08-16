using Microsoft.EntityFrameworkCore.Migrations;

namespace Vantigo.Tenancy.EntityFramework;

/// <summary>
/// Emits PostgreSQL row-level security migration operations for tenant-owned tables.
/// </summary>
public static class TenantRlsMigrationExtensions
{
    /// <summary>
    /// Enables and forces tenant RLS, with an unset tenant GUC matching no rows.
    /// </summary>
    public static MigrationBuilder EnableTenantRls(
        this MigrationBuilder migrationBuilder,
        string schema,
        string table)
    {
        ArgumentNullException.ThrowIfNull(migrationBuilder);
        var qualifiedTable = TenantSqlIdentifiers.QuoteQualifiedTable(schema, table);

        migrationBuilder.Sql($"""
            ALTER TABLE {qualifiedTable} ENABLE ROW LEVEL SECURITY;
            ALTER TABLE {qualifiedTable} FORCE ROW LEVEL SECURITY;
            CREATE POLICY tenant_isolation ON {qualifiedTable}
                USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
            """);
        return migrationBuilder;
    }

    /// <summary>
    /// Removes the tenant policy and disables tenant RLS for a table.
    /// </summary>
    public static MigrationBuilder DisableTenantRls(
        this MigrationBuilder migrationBuilder,
        string schema,
        string table)
    {
        ArgumentNullException.ThrowIfNull(migrationBuilder);
        var qualifiedTable = TenantSqlIdentifiers.QuoteQualifiedTable(schema, table);

        migrationBuilder.Sql($"""
            DROP POLICY IF EXISTS tenant_isolation ON {qualifiedTable};
            ALTER TABLE {qualifiedTable} NO FORCE ROW LEVEL SECURITY;
            ALTER TABLE {qualifiedTable} DISABLE ROW LEVEL SECURITY;
            """);
        return migrationBuilder;
    }

    /// <summary>
    /// Adds the schema-local tenant counter table and protects it with tenant RLS.
    /// </summary>
    public static MigrationBuilder AddTenantCountersTable(
        this MigrationBuilder migrationBuilder,
        string schema)
    {
        ArgumentNullException.ThrowIfNull(migrationBuilder);
        var qualifiedTable = TenantSqlIdentifiers.QuoteQualifiedTable(schema, "tenant_counters");

        migrationBuilder.Sql($"""
            CREATE TABLE {qualifiedTable} (
                tenant_id uuid NOT NULL,
                counter_name text NOT NULL,
                next_value bigint NOT NULL,
                CONSTRAINT pk_tenant_counters PRIMARY KEY (tenant_id, counter_name)
            );
            """);
        migrationBuilder.EnableTenantRls(schema, "tenant_counters");
        return migrationBuilder;
    }
}

internal static class TenantSqlIdentifiers
{
    internal static string QuoteQualifiedTable(string schema, string table)
    {
        if (string.IsNullOrWhiteSpace(schema))
            throw new ArgumentException("A schema is required.", nameof(schema));
        if (string.IsNullOrWhiteSpace(table))
            throw new ArgumentException("A table is required.", nameof(table));

        return $"{QuoteIdentifier(schema)}.{QuoteIdentifier(table)}";
    }

    internal static string QuoteIdentifier(string identifier) =>
        $"\"{identifier.Replace("\"", "\"\"", StringComparison.Ordinal)}\"";
}