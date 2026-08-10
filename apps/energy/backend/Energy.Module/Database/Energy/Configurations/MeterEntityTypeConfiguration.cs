using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Energy.Domain.Meters;

namespace Vantigo.Energy.Database.Energy.Configurations;

internal sealed class MeterEntityTypeConfiguration : IEntityTypeConfiguration<Meter>
{
    public void Configure(EntityTypeBuilder<Meter> builder)
    {
        builder.ToTable("meters");
        builder.HasKey(meter => meter.Id);
        builder.Property(meter => meter.Id).HasColumnName("id").IsRequired().HasIdentityOptions(1001, 1);
        builder.Property(meter => meter.MeteringPointId).HasColumnName("metering_point_id").IsRequired();
        builder.Property(meter => meter.MeterNumber).HasColumnName("meter_number").HasMaxLength(64).IsRequired();
        builder.Property(meter => meter.InstalledAt).HasColumnName("installed_at").IsRequired();
        builder.Property(meter => meter.RemovedAt).HasColumnName("removed_at");
        builder.HasOne(meter => meter.MeteringPoint).WithMany(point => point.Meters)
            .HasForeignKey(meter => meter.MeteringPointId).OnDelete(DeleteBehavior.Cascade);
        builder.HasIndex(meter => meter.MeteringPointId).IsUnique().HasFilter("removed_at IS NULL");
    }
}