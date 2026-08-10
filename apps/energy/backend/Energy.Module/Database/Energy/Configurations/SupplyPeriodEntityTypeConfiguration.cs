using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Energy.Domain.SupplyPeriods;

namespace Vantigo.Energy.Database.Energy.Configurations;

internal sealed class SupplyPeriodEntityTypeConfiguration : IEntityTypeConfiguration<SupplyPeriod>
{
    public void Configure(EntityTypeBuilder<SupplyPeriod> builder)
    {
        builder.ToTable("supply_periods");
        builder.HasKey(period => period.Id);
        builder.Property(period => period.Id).HasColumnName("id").IsRequired().HasIdentityOptions(1001, 1);
        builder.Property(period => period.MeteringPointId).HasColumnName("metering_point_id").IsRequired();
        builder.Property(period => period.CustomerId).HasColumnName("customer_id").IsRequired();
        builder.Property(period => period.Start).HasColumnName("start").IsRequired();
        builder.Property(period => period.End).HasColumnName("end");
        builder.Property(period => period.Status).HasColumnName("status").HasConversion<string>().HasMaxLength(20).IsUnicode(false).IsRequired();
        builder.HasOne(period => period.MeteringPoint).WithMany(point => point.SupplyPeriods)
            .HasForeignKey(period => period.MeteringPointId).OnDelete(DeleteBehavior.Cascade);
        builder.HasIndex(period => period.MeteringPointId);
    }
}