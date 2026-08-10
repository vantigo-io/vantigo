using Npgsql;

namespace Vantigo.Energy.Database;

internal static class DesignTimeNpgsqlDataSource
{
    internal static NpgsqlDataSource Create(string database)
    {
        var connectionString = Environment.GetEnvironmentVariable("ConnectionStrings__vantigo") ??
            $"Host=127.0.0.1;Port=1;Database={database};Timeout=1";
        var builder = new NpgsqlDataSourceBuilder(connectionString);
        builder.EnableDynamicJson();
        return builder.Build();
    }
}