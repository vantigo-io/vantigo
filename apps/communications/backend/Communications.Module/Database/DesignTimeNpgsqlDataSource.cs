using Npgsql;

namespace Vantigo.Communications.Database;

internal static class DesignTimeNpgsqlDataSource
{
    internal static NpgsqlDataSource Create(string database)
    {
        var connectionString = Environment.GetEnvironmentVariable("ConnectionStrings__vantigo") ??
            $"Host=127.0.0.1;Port=1;Database={database};Timeout=1";
        return NpgsqlDataSource.Create(connectionString);
    }
}