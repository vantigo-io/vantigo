using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Products.Domain.Products;

namespace Vantigo.Products.Database.Products.Configurations;

internal sealed class ProductEntityTypeConfiguration : IEntityTypeConfiguration<Product>
{
    public void Configure(EntityTypeBuilder<Product> builder)
    {
        builder.ToTable("products");

        builder.HasKey(p => p.Id);

        builder.Property(p => p.Id)
            .HasColumnName("id")
            .HasComment("The unique identifier of the product")
            .IsRequired()
            .HasIdentityOptions(1001, 1);

        builder.Property(p => p.Name)
            .HasColumnName("name")
            .HasComment("The display name of the product")
            .HasMaxLength(Product.NameMaxLength)
            .IsUnicode(true)
            .IsRequired();

        builder.Property(p => p.Description)
            .HasColumnName("description")
            .HasComment("A free-form plain-text description of the product")
            .HasMaxLength(Product.DescriptionMaxLength)
            .IsUnicode(true);

        builder.Property(p => p.CategoryId)
            .HasColumnName("category_id")
            .HasComment("The category the product belongs to; null means uncategorised");

        builder.HasOne(p => p.Category)
            .WithMany()
            .HasForeignKey(p => p.CategoryId)
            .OnDelete(DeleteBehavior.Restrict);

        builder.Property(p => p.TaxCategoryId)
            .HasColumnName("tax_category_id")
            .HasComment("The tax category applied when the product is sold")
            .IsRequired();

        builder.HasOne(p => p.TaxCategory)
            .WithMany()
            .HasForeignKey(p => p.TaxCategoryId)
            .OnDelete(DeleteBehavior.Restrict);

        builder.Property(p => p.Type)
            .HasColumnName("type")
            .HasComment("Whether the product is a physical good or a performed service")
            .HasConversion<string>()
            .HasMaxLength(20)
            .IsUnicode(false)
            .IsRequired();

        builder.Property(p => p.Status)
            .HasColumnName("status")
            .HasComment("The lifecycle status of the product")
            .HasConversion<string>()
            .HasMaxLength(20)
            .IsUnicode(false)
            .IsRequired();

        builder.Property(p => p.CreatedAt)
            .HasColumnName("created_at")
            .HasComment("When the product was created")
            .IsRequired();

        builder.Property(p => p.UpdatedAt)
            .HasColumnName("updated_at")
            .HasComment("When the product was last updated")
            .IsRequired();

        builder.HasMany(p => p.Variants)
            .WithOne()
            .HasForeignKey(variant => variant.ProductId)
            .OnDelete(DeleteBehavior.Cascade);
    }
}