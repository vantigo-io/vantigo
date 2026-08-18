using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Customers.Domain.Customers;
using Vantigo.Customers.Domain.Customers.Common;

namespace Vantigo.Customers.Database.Customers.Configurations;

internal sealed class CustomerEntityTypeConfiguration : IEntityTypeConfiguration<Customer>
{
    public void Configure(EntityTypeBuilder<Customer> builder)
    {
        builder.ToTable("customers");

        builder.HasKey(c => new { c.TenantId, c.Id });

        builder.Property(c => c.TenantId)
            .HasColumnName("tenant_id")
            .IsRequired();

        builder.Property(c => c.CustomerNumber)
            .HasColumnName("customer_number")
            .IsRequired();

        builder.HasIndex(c => new { c.TenantId, c.CustomerNumber })
            .IsUnique()
            .HasDatabaseName("ux_customers_tenant_customer_number");

        builder.Property(c => c.Id)
            .HasColumnName("id")
            .HasComment("The unique identifier of the customer")
            .IsRequired()
            .ValueGeneratedOnAdd()
            .HasIdentityOptions(1001, 1);

        builder.Property(c => c.Name)
            .HasColumnName("name")
            .HasComment("The friendly name of the customer")
            .HasConversion(name => name.ToPersistence(), value => FriendlyName.FromPersistence(value))
            .HasMaxLength(FriendlyName.MaxLength)
            .IsUnicode(true)
            .IsRequired();

        builder.Property(c => c.Status)
            .HasColumnName("status")
            .HasComment("The lifecycle status of the customer")
            .HasConversion(status => status.ToPersistence(), value => CustomerStatus.FromPersistence(value))
            .HasMaxLength(20)
            .IsUnicode(false)
            .HasDefaultValue((CustomerStatus)CustomerStatus.Active)
            .IsRequired();

        builder.Property(c => c.CreatedAt)
            .HasColumnName("created_at")
            .HasComment("When the customer was first persisted")
            .IsRequired();

        builder.Property(c => c.UpdatedAt)
            .HasColumnName("updated_at")
            .HasComment("When the customer row itself was last changed")
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

            identity.Property(i => i.Source)
                .HasColumnName("legal_source")
                .HasComment("Where the legal identity data of the customer was retrieved from")
                .HasConversion(source => source.ToPersistence(), value => LegalSource.FromPersistence(value))
                .HasMaxLength(50)
                .IsUnicode(false);
        });
    }
}