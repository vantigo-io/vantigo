using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Energy.Domain.SupplyPeriods;

namespace Vantigo.Energy.Database.Energy.Configurations;

internal sealed class SupplyPeriodEntityTypeConfiguration : IEntityTypeConfiguration<SupplyPeriod>
{
    public void Configure(EntityTypeBuilder<SupplyPeriod> builder)
    {
        builder.ToTable("supply_periods");
        builder.HasKey(period => new { period.TenantId, period.Id });
        builder.Property(period => period.TenantId).HasColumnName("tenant_id").IsRequired();
        builder.Property(period => period.Id).HasColumnName("id").ValueGeneratedOnAdd().IsRequired().HasIdentityOptions(1001, 1);
        builder.Property(period => period.MeteringPointId).HasColumnName("metering_point_id").IsRequired();
        builder.Property(period => period.CustomerId).HasColumnName("customer_id").IsRequired();
        builder.Property(period => period.Start).HasColumnName("start").IsRequired();
        builder.Property(period => period.End).HasColumnName("end");
        builder.Property(period => period.Status).HasColumnName("status").HasConversion<string>().HasMaxLength(20).IsUnicode(false).IsRequired();
        builder.HasOne(period => period.MeteringPoint).WithMany(point => point.SupplyPeriods)
            .HasForeignKey(period => new { period.TenantId, period.MeteringPointId })
            .HasPrincipalKey(point => new { point.TenantId, point.Id })
            .OnDelete(DeleteBehavior.Cascade);
        builder.HasIndex(period => new { period.TenantId, period.MeteringPointId });
    }
}