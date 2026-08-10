using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Products.Domain.Products;

namespace Vantigo.Products.Database.Products.Configurations;

internal sealed class TaxCategoryEntityTypeConfiguration : IEntityTypeConfiguration<TaxCategory>
{
    public void Configure(EntityTypeBuilder<TaxCategory> builder)
    {
        builder.ToTable("tax_categories");

        builder.HasKey(category => category.Id);

        builder.Property(category => category.Id)
            .HasColumnName("id")
            .HasComment("The unique identifier of the tax category")
            .IsRequired()
            .HasIdentityOptions(1001, 1);

        builder.Property(category => category.Name)
            .HasColumnName("name")
            .HasComment("The unique display name of the tax category")
            .HasMaxLength(TaxCategory.NameMaxLength)
            .IsUnicode(true)
            .IsRequired();

        builder.Property(category => category.Kind)
            .HasColumnName("kind")
            .HasComment("The tax treatment kind")
            .HasConversion<string>()
            .HasMaxLength(20)
            .IsUnicode(false)
            .IsRequired();

        builder.Property(category => category.Rate)
            .HasColumnName("rate")
            .HasComment("The tax rate as a fraction, for example 0.25 for 25%")
            .HasPrecision(5, 4)
            .IsRequired();

        builder.Property(category => category.CreatedAt)
            .HasColumnName("created_at")
            .HasComment("When the tax category was created")
            .IsRequired();

        builder.Property(category => category.UpdatedAt)
            .HasColumnName("updated_at")
            .HasComment("When the tax category was last updated")
            .IsRequired();

        builder.HasIndex(category => category.Name).IsUnique();
    }
}