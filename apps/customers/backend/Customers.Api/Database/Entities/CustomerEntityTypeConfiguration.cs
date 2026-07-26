using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Customers.Api.Domain.Customers;
using Vantigo.Customers.Api.Domain.Customers.Common;

namespace Vantigo.Customers.Api.Database.Entities;

internal sealed class CustomerEntityTypeConfiguration : IEntityTypeConfiguration<Customer>
{
    public void Configure(EntityTypeBuilder<Customer> builder)
    {
        builder.ToTable("customers");

        builder.HasKey(c => c.Id);

        builder.Property(c => c.Id)
            .HasColumnName("id")
            .HasComment("The unique identifier of the customer")
            .IsRequired()
            .HasIdentityOptions(1001, 1);

        builder.Property(c => c.Name)
            .HasColumnName("name")
            .HasComment("The friendly name of the customer")
            .HasConversion(name => name.ToPersistence(), value => FriendlyName.FromPersistence(value))
            .HasMaxLength(FriendlyName.MaxLength)
            .IsUnicode(true)
            .IsRequired();

        builder.ComplexProperty(c => c.Identity, identity =>
        {
            identity.Property(i => i.Country)
                .HasColumnName("legal_country")
                .HasComment("The legal country of the identity that is associated with the customer")
                .HasConversion(country => country.ToPersistence(), value => CountryCode.FromPersistence(value))
                .HasMaxLength(2)
                .IsUnicode(false);

            identity.Property(i => i.Type)
                .HasColumnName("legal_type")
                .HasComment("The legal type of the identity that is associated with the customer")
                .HasConversion(type => type.ToPersistence(), value => LegalType.FromPersistence(value))
                .HasMaxLength(50)
                .IsUnicode(false);

            identity.Property(i => i.Id)
                .HasColumnName("legal_id")
                .HasComment("The legal id of the identity that is associated with the customer")
                .HasConversion(id => id.ToPersistence(), value => LegalId.FromPersistence(value))
                .HasMaxLength(LegalId.MaxLength)
                .IsUnicode(false);

            identity.Property(i => i.Name)
                .HasColumnName("legal_name")
                .HasComment("The legal name of the identity that is associated with the customer")
                .HasConversion(name => name.ToPersistence(), value => LegalName.FromPersistence(value))
                .HasMaxLength(LegalName.MaxLength)
                .IsUnicode(true);
        });
    }
}
