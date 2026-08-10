using Npgsql;

namespace Vantigo.Products.Database;

/// <summary>
/// Supplies EF tooling with a non-production data source when the CLI is run
/// without application configuration. Migration discovery and SQL generation do
/// not require a live database; the deliberately unreachable fallback prevents
/// design-time commands from silently targeting a real external database.
/// </summary>
internal static class DesignTimeNpgsqlDataSource
{
    internal static NpgsqlDataSource Create(string database)
    {
        var connectionString = Environment.GetEnvironmentVariable("ConnectionStrings__vantigo") ??
            $"Host=127.0.0.1;Port=1;Database={database};Timeout=1";
        var dataSourceBuilder = new NpgsqlDataSourceBuilder(connectionString);
        dataSourceBuilder.EnableDynamicJson();
        return dataSourceBuilder.Build();
    }
}