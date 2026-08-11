using Microsoft.AspNetCore.DataProtection.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore;

using Vantigo.Configuration;

namespace Vantigo.DataProtection.PostgreSql;

/// <summary>
/// Entity Framework Core context that stores ASP.NET Core Data Protection key
/// material in PostgreSQL. This is the shared key ring for the whole Vantigo
/// host; all replicas must point at the same database so cookies, antiforgery
/// tokens, and protected payloads remain valid across processes.
/// </summary>
public sealed class DataProtectionKeyDbContext : DbContext, IDataProtectionKeyContext
{
    public DataProtectionKeyDbContext(DbContextOptions<DataProtectionKeyDbContext> options)
        : base(options)
    {
    }

    /// <summary>
    /// The data protection keys table, required by <see cref="IDataProtectionKeyContext"/>.
    /// </summary>
    public DbSet<DataProtectionKey> DataProtectionKeys { get; set; } = default!;

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        base.OnModelCreating(modelBuilder);

        modelBuilder.Entity<DataProtectionKey>(entity =>
        {
            entity.ToTable(DataProtectionPostgreSqlOptions.DefaultTableName, DataProtectionPostgreSqlOptions.DefaultSchema);
            entity.HasKey(e => e.Id);
            entity.Property(e => e.FriendlyName).HasMaxLength(449);
            entity.Property(e => e.Xml).HasColumnType("text");
        });
    }

    internal static void ConfigureModel(ModelBuilder modelBuilder, DataProtectionPostgreSqlOptions options)
    {
        modelBuilder.Entity<DataProtectionKey>(entity =>
        {
            entity.ToTable(options.TableName, options.Schema);
            entity.HasKey(e => e.Id);
            entity.Property(e => e.FriendlyName).HasMaxLength(449);
            entity.Property(e => e.Xml).HasColumnType("text");
        });
    }
}