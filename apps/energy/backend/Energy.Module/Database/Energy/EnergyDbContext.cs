using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy.Configurations;
using Vantigo.Energy.Domain.Consumption;
using Vantigo.Energy.Domain.MeteringPoints;
using Vantigo.Energy.Domain.Meters;
using Vantigo.Energy.Domain.SupplyPeriods;

namespace Vantigo.Energy.Database.Energy;

public sealed class EnergyDbContext(DbContextOptions<EnergyDbContext> options) : DbContext(options)
{
    public DbSet<MeteringPoint> MeteringPoints => Set<MeteringPoint>();
    public DbSet<Meter> Meters => Set<Meter>();
    public DbSet<ConsumptionInterval> ConsumptionIntervals => Set<ConsumptionInterval>();
    public DbSet<SupplyPeriod> SupplyPeriods => Set<SupplyPeriod>();

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        modelBuilder.HasDefaultSchema("energy");
        modelBuilder.ApplyConfiguration(new MeteringPointEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new MeterEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new ConsumptionIntervalEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new SupplyPeriodEntityTypeConfiguration());
    }

    public override int SaveChanges(bool acceptAllChangesOnSuccess)
    {
        StampTimestamps();
        return base.SaveChanges(acceptAllChangesOnSuccess);
    }

    public override Task<int> SaveChangesAsync(bool acceptAllChangesOnSuccess, CancellationToken cancellationToken = default)
    {
        StampTimestamps();
        return base.SaveChangesAsync(acceptAllChangesOnSuccess, cancellationToken);
    }

    private void StampTimestamps()
    {
        var now = DateTimeOffset.UtcNow;
        foreach (var entry in ChangeTracker.Entries<MeteringPoint>())
        {
            if (entry.State == EntityState.Added)
            {
                entry.Entity.CreatedAt = now;
                entry.Entity.UpdatedAt = now;
            }
            else if (entry.State == EntityState.Modified)
            {
                entry.Entity.UpdatedAt = now;
            }
        }
    }
}