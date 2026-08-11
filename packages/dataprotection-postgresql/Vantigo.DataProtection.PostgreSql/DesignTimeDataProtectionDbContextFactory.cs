using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Design;

namespace Vantigo.DataProtection.PostgreSql;

/// <summary>
/// EF Core design-time factory for the data protection keys context. Used by
/// the <c>dotnet ef migrations</c> tooling.
/// </summary>
internal sealed class DesignTimeDataProtectionDbContextFactory : IDesignTimeDbContextFactory<DataProtectionKeyDbContext>
{
    public DataProtectionKeyDbContext CreateDbContext(string[] args)
    {
        var connectionString = Environment.GetEnvironmentVariable("ConnectionStrings__vantigo")
            ?? Environment.GetEnvironmentVariable("ConnectionStrings__customers")
            ?? "Host=127.0.0.1;Port=1;Database=vantigo;Timeout=1";

        var optionsBuilder = new DbContextOptionsBuilder<DataProtectionKeyDbContext>();
        optionsBuilder.UseNpgsql(connectionString);

        return new DataProtectionKeyDbContext(optionsBuilder.Options);
    }
}