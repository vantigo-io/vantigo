using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Energy.Domain.MeteringPoints;

namespace Vantigo.Energy.Database.Energy.Configurations;

internal sealed class MeteringPointEntityTypeConfiguration : IEntityTypeConfiguration<MeteringPoint>
{
    public void Configure(EntityTypeBuilder<MeteringPoint> builder)
    {
        builder.ToTable("metering_points");
        builder.HasKey(point => point.Id);
        builder.Property(point => point.Id).HasColumnName("id").IsRequired().HasIdentityOptions(1001, 1);
        builder.Property(point => point.Gsrn).HasColumnName("gsrn")
            .HasConversion(value => value.Value, value => new Gsrn(value)).HasMaxLength(18).IsUnicode(false).IsRequired();
        builder.Property(point => point.PriceArea).HasColumnName("price_area").HasMaxLength(4).IsUnicode(false).IsRequired();
        builder.Property(point => point.GridArea).HasColumnName("grid_area").HasMaxLength(64).IsUnicode(false);
        builder.Property(point => point.ExpectedAnnualConsumptionKwh).HasColumnName("expected_annual_consumption_kwh").HasPrecision(14, 3);
        builder.Property(point => point.Latitude).HasColumnName("latitude").HasPrecision(9, 6);
        builder.Property(point => point.Longitude).HasColumnName("longitude").HasPrecision(9, 6);
        builder.Property(point => point.ConnectionStatus).HasColumnName("connection_status").HasConversion<string>().HasMaxLength(20).IsUnicode(false).IsRequired();
        builder.Property(point => point.CreatedAt).HasColumnName("created_at").IsRequired();
        builder.Property(point => point.UpdatedAt).HasColumnName("updated_at").IsRequired();
        builder.OwnsOne(point => point.Address, address =>
        {
            address.Property(value => value.StreetAddress).HasColumnName("street_address").HasMaxLength(Address.StreetAddressMaxLength).IsRequired();
            address.Property(value => value.PostalCode).HasColumnName("postal_code").HasMaxLength(Address.PostalCodeMaxLength).IsRequired();
            address.Property(value => value.City).HasColumnName("city").HasMaxLength(Address.CityMaxLength).IsRequired();
            address.Property(value => value.CountryCode).HasColumnName("country_code").HasMaxLength(2).IsUnicode(false).IsRequired();
        });
        builder.HasIndex(point => point.Gsrn).IsUnique();
    }
}