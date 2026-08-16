using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Customers.Domain.Contacts;
using Vantigo.Customers.Domain.Contacts.Common;

namespace Vantigo.Customers.Database.Customers.Configurations;

internal sealed class CustomerContactEntityTypeConfiguration : IEntityTypeConfiguration<CustomerContact>
{
    public void Configure(EntityTypeBuilder<CustomerContact> builder)
    {
        builder.ToTable("customers_contacts");

        builder.HasKey(cc => new { cc.TenantId, cc.CustomerId, cc.ContactId });

        builder.Property(cc => cc.TenantId)
            .HasColumnName("tenant_id")
            .IsRequired();

        builder.Property(cc => cc.CustomerId)
            .HasColumnName("customer_id")
            .HasComment("The id of the customer the contact is associated with")
            .IsRequired();

        builder.Property(cc => cc.ContactId)
            .HasColumnName("contact_id")
            .HasComment("The id of the associated contact")
            .IsRequired();

        builder.Property(cc => cc.Role)
            .HasColumnName("role")
            .HasComment("The role the contact holds for the customer, such as 'CEO' or 'Custodian'")
            .HasConversion(role => role.ToPersistence(), value => ContactRole.FromPersistence(value))
            .HasMaxLength(ContactRole.MaxLength)
            .IsUnicode(true)
            .IsRequired();

        builder.Property(cc => cc.Phone)
            .HasColumnName("phone")
            .HasComment("A connection-specific phone number, when it differs from the contact's own")
            .HasConversion(phone => phone!.Value.ToPersistence(), value => PhoneNumber.FromPersistence(value))
            .HasMaxLength(PhoneNumber.MaxLength)
            .IsUnicode(false);

        builder.Property(cc => cc.Email)
            .HasColumnName("email")
            .HasComment("A connection-specific email address, when it differs from the contact's own")
            .HasConversion(email => email!.Value.ToPersistence(), value => EmailAddress.FromPersistence(value))
            .HasMaxLength(EmailAddress.MaxLength)
            .IsUnicode(false);

        builder.HasOne(cc => cc.Customer)
            .WithMany()
            .HasForeignKey(cc => new { cc.TenantId, cc.CustomerId })
            .HasPrincipalKey(customer => new { customer.TenantId, customer.Id })
            .OnDelete(DeleteBehavior.Cascade);

        builder.HasOne(cc => cc.Contact)
            .WithMany()
            .HasForeignKey(cc => new { cc.TenantId, cc.ContactId })
            .HasPrincipalKey(contact => new { contact.TenantId, contact.Id })
            .OnDelete(DeleteBehavior.Cascade);

        builder.HasIndex(cc => new { cc.TenantId, cc.ContactId })
            .HasDatabaseName("ix_customers_contacts_tenant_contact_id");
    }
}