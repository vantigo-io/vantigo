using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Customers.Domain.Contacts;
using Vantigo.Customers.Domain.Contacts.Common;

namespace Vantigo.Customers.Database.Customers.Configurations;

internal sealed class ContactEntityTypeConfiguration : IEntityTypeConfiguration<Contact>
{
    public void Configure(EntityTypeBuilder<Contact> builder)
    {
        builder.ToTable("contacts");

        builder.HasKey(c => c.Id);

        builder.Property(c => c.Id)
            .HasColumnName("id")
            .HasComment("The unique identifier of the contact")
            .IsRequired()
            .HasIdentityOptions(1001, 1);

        builder.Property(c => c.FirstName)
            .HasColumnName("first_name")
            .HasComment("The given name of the contact")
            .HasConversion(name => name.ToPersistence(), value => PersonName.FromPersistence(value))
            .HasMaxLength(PersonName.MaxLength)
            .IsUnicode(true)
            .IsRequired();

        builder.Property(c => c.LastName)
            .HasColumnName("last_name")
            .HasComment("The family name of the contact")
            .HasConversion(name => name.ToPersistence(), value => PersonName.FromPersistence(value))
            .HasMaxLength(PersonName.MaxLength)
            .IsUnicode(true)
            .IsRequired();

        builder.Property(c => c.MiddleName)
            .HasColumnName("middle_name")
            .HasComment("The middle name of the contact, when known")
            .HasConversion(name => name!.Value.ToPersistence(), value => PersonName.FromPersistence(value))
            .HasMaxLength(PersonName.MaxLength)
            .IsUnicode(true);

        builder.Property(c => c.Prefix)
            .HasColumnName("prefix")
            .HasComment("An honorific prefix such as 'Dr.', when known")
            .HasConversion(part => part!.Value.ToPersistence(), value => NamePart.FromPersistence(value))
            .HasMaxLength(NamePart.MaxLength)
            .IsUnicode(true);

        builder.Property(c => c.Suffix)
            .HasColumnName("suffix")
            .HasComment("An honorific suffix such as 'Jr.' or 'PhD', when known")
            .HasConversion(part => part!.Value.ToPersistence(), value => NamePart.FromPersistence(value))
            .HasMaxLength(NamePart.MaxLength)
            .IsUnicode(true);

        builder.Property(c => c.Phone)
            .HasColumnName("phone")
            .HasComment("The phone number the contact can be reached on, when known")
            .HasConversion(phone => phone!.Value.ToPersistence(), value => PhoneNumber.FromPersistence(value))
            .HasMaxLength(PhoneNumber.MaxLength)
            .IsUnicode(false);

        builder.Property(c => c.Email)
            .HasColumnName("email")
            .HasComment("The email address the contact can be reached on, when known")
            .HasConversion(email => email!.Value.ToPersistence(), value => EmailAddress.FromPersistence(value))
            .HasMaxLength(EmailAddress.MaxLength)
            .IsUnicode(false);
    }
}