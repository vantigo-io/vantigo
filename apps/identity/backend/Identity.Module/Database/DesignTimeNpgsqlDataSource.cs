using Npgsql;

namespace Vantigo.Identity.Database;

internal static class DesignTimeNpgsqlDataSource
{
    internal static NpgsqlDataSource Create(string database)
    {
        var connectionString = Environment.GetEnvironmentVariable("ConnectionStrings__vantigo") ??
            Environment.GetEnvironmentVariable("ConnectionStrings__customers") ??
            $"Host=127.0.0.1;Port=1;Database={database};Timeout=1";
        return NpgsqlDataSource.Create(connectionString);
    }
}