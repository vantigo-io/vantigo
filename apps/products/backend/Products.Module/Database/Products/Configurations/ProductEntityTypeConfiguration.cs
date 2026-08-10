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

        builder.Property(p => p.Sku)
            .HasColumnName("sku")
            .HasComment("The stock keeping unit uniquely identifying this sellable unit")
            .HasMaxLength(Product.SkuMaxLength)
            .IsUnicode(false)
            .IsRequired();

        builder.HasIndex(p => p.Sku)
            .IsUnique();

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

        builder.Property(p => p.Barcode)
            .HasColumnName("barcode")
            .HasComment("The GTIN barcode (GTIN-8/12/13/14) of the product, unique when set")
            .HasMaxLength(Gtin.MaxLength)
            .IsUnicode(false);

        builder.HasIndex(p => p.Barcode)
            .IsUnique()
            .HasFilter("barcode IS NOT NULL");

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

        builder.Property(p => p.Unit)
            .HasColumnName("unit")
            .HasComment("The unit the product is sold in, for instance pcs or hour")
            .HasMaxLength(Product.UnitMaxLength)
            .IsUnicode(false)
            .IsRequired();

        builder.Property(p => p.StandardCost)
            .HasColumnName("standard_cost")
            .HasComment("An indicative cost in the company base currency excluding VAT, used for margin estimates")
            .HasPrecision(12, 2);

        builder.Property(p => p.VatRate)
            .HasColumnName("vat_rate")
            .HasComment("The VAT rate applied when the product is sold, for instance 0.25 for 25%")
            .HasPrecision(5, 4)
            .IsRequired();

        builder.Property(p => p.WeightKg)
            .HasColumnName("weight_kg")
            .HasComment("The gross weight of one unit in kilograms")
            .HasPrecision(10, 3);

        builder.Property(p => p.LengthCm)
            .HasColumnName("length_cm")
            .HasComment("The length of one unit in centimetres")
            .HasPrecision(10, 1);

        builder.Property(p => p.WidthCm)
            .HasColumnName("width_cm")
            .HasComment("The width of one unit in centimetres")
            .HasPrecision(10, 1);

        builder.Property(p => p.HeightCm)
            .HasColumnName("height_cm")
            .HasComment("The height of one unit in centimetres")
            .HasPrecision(10, 1);

        builder.Property(p => p.CreatedAt)
            .HasColumnName("created_at")
            .HasComment("When the product was created")
            .IsRequired();

        builder.Property(p => p.UpdatedAt)
            .HasColumnName("updated_at")
            .HasComment("When the product was last updated")
            .IsRequired();

        builder.HasMany(p => p.Prices)
            .WithOne()
            .HasForeignKey(price => price.ProductId)
            .OnDelete(DeleteBehavior.Cascade);
    }
}