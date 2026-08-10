using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Energy.Domain.Consumption;

namespace Vantigo.Energy.Database.Energy.Configurations;

internal sealed class ConsumptionIntervalEntityTypeConfiguration : IEntityTypeConfiguration<ConsumptionInterval>
{
    public void Configure(EntityTypeBuilder<ConsumptionInterval> builder)
    {
        builder.ToTable("consumption_intervals");
        builder.HasKey(interval => interval.Id);
        builder.Property(interval => interval.Id).HasColumnName("id").IsRequired();
        builder.Property(interval => interval.MeteringPointId).HasColumnName("metering_point_id").IsRequired();
        builder.Property(interval => interval.Start).HasColumnName("start").IsRequired();
        builder.Property(interval => interval.End).HasColumnName("end").IsRequired();
        builder.Property(interval => interval.QuantityKwh).HasColumnName("quantity_kwh").HasPrecision(14, 3).IsRequired();
        builder.Property(interval => interval.Quality).HasColumnName("quality").HasConversion<string>().HasMaxLength(20).IsUnicode(false).IsRequired();
        builder.Property(interval => interval.Source).HasColumnName("source").HasConversion<string>().HasMaxLength(20).IsUnicode(false).IsRequired();
        builder.Property(interval => interval.ReceivedAt).HasColumnName("received_at").IsRequired();
        builder.Property(interval => interval.IsCurrent).HasColumnName("is_current").IsRequired();
        builder.Property(interval => interval.SupersedesId).HasColumnName("supersedes_id");
        builder.HasOne(interval => interval.MeteringPoint).WithMany(point => point.ConsumptionIntervals)
            .HasForeignKey(interval => interval.MeteringPointId).OnDelete(DeleteBehavior.Cascade);
        builder.HasOne(interval => interval.Supersedes).WithMany().HasForeignKey(interval => interval.SupersedesId).OnDelete(DeleteBehavior.Restrict);
        builder.HasIndex(interval => new { interval.MeteringPointId, interval.Start, interval.End })
            .IsUnique()
            .HasFilter("is_current = TRUE");
    }
}