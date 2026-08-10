using System.Text.Json;

using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.ChangeTracking;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Products.Domain.Products;

namespace Vantigo.Products.Database.Products.Configurations;

internal sealed class ProductVariantEntityTypeConfiguration : IEntityTypeConfiguration<ProductVariant>
{
    public void Configure(EntityTypeBuilder<ProductVariant> builder)
    {
        builder.ToTable("product_variants");

        builder.HasKey(v => v.Id);

        builder.Property(v => v.Id)
            .HasColumnName("id")
            .HasComment("The unique identifier of the product variant")
            .IsRequired()
            .HasIdentityOptions(1001, 1);

        builder.Property(v => v.ProductId)
            .HasColumnName("product_id")
            .HasComment("The product that owns the variant")
            .IsRequired();

        builder.Property(v => v.Sku)
            .HasColumnName("sku")
            .HasComment("The stock keeping unit uniquely identifying this sellable variant")
            .HasMaxLength(ProductVariant.SkuMaxLength)
            .IsUnicode(false)
            .IsRequired();

        builder.HasIndex(v => v.Sku).IsUnique();

        builder.Property(v => v.Barcode)
            .HasColumnName("barcode")
            .HasComment("The GTIN barcode of the variant, unique when set")
            .HasMaxLength(Gtin.MaxLength)
            .IsUnicode(false);

        builder.HasIndex(v => v.Barcode)
            .IsUnique()
            .HasFilter("barcode IS NOT NULL");

        builder.Property(v => v.Unit)
            .HasColumnName("unit")
            .HasComment("The unit the variant is sold in, for instance pcs or hour")
            .HasMaxLength(ProductVariant.UnitMaxLength)
            .IsUnicode(false)
            .IsRequired();

        builder.Property(v => v.StandardCost)
            .HasColumnName("standard_cost")
            .HasComment("An indicative cost in the company base currency excluding VAT")
            .HasPrecision(12, 2);

        builder.Property(v => v.WeightKg)
            .HasColumnName("weight_kg")
            .HasComment("The gross weight of one unit in kilograms")
            .HasPrecision(10, 3);

        builder.Property(v => v.LengthCm)
            .HasColumnName("length_cm")
            .HasComment("The length of one unit in centimetres")
            .HasPrecision(10, 1);

        builder.Property(v => v.WidthCm)
            .HasColumnName("width_cm")
            .HasComment("The width of one unit in centimetres")
            .HasPrecision(10, 1);

        builder.Property(v => v.HeightCm)
            .HasColumnName("height_cm")
            .HasComment("The height of one unit in centimetres")
            .HasPrecision(10, 1);

        builder.Property(v => v.OptionValues)
            .HasColumnName("option_values")
            .HasComment("The option values distinguishing this variant")
            .HasColumnType("jsonb")
            // The host owns the shared Npgsql data source, so use an explicit JSON
            // conversion instead of requiring dynamic JSON opt-in globally.
            .HasConversion(
                values => JsonSerializer.Serialize(values, (JsonSerializerOptions?)null),
                json => JsonSerializer.Deserialize<Dictionary<string, string>>(json, (JsonSerializerOptions?)null) ?? new Dictionary<string, string>(),
                new ValueComparer<Dictionary<string, string>>(
                    (left, right) => DictionariesEqual(left, right),
                    values => DictionaryHashCode(values),
                    values => new Dictionary<string, string>(values)))
            .IsRequired();

        builder.Property(v => v.CreatedAt)
            .HasColumnName("created_at")
            .HasComment("When the variant was created")
            .IsRequired();

        builder.Property(v => v.UpdatedAt)
            .HasColumnName("updated_at")
            .HasComment("When the variant was last updated")
            .IsRequired();

        builder.HasOne<Product>()
            .WithMany(p => p.Variants)
            .HasForeignKey(v => v.ProductId)
            .OnDelete(DeleteBehavior.Cascade);

        builder.HasMany(v => v.Prices)
            .WithOne()
            .HasForeignKey(price => price.VariantId)
            .OnDelete(DeleteBehavior.Cascade);
    }

    private static bool DictionariesEqual(
        Dictionary<string, string>? left,
        Dictionary<string, string>? right) =>
        left is not null && right is not null && left.Count == right.Count &&
        left.All(pair => right.TryGetValue(pair.Key, out var value) && value == pair.Value);

    private static int DictionaryHashCode(Dictionary<string, string> values) =>
        values.OrderBy(pair => pair.Key).Aggregate(
            0, (hash, pair) => HashCode.Combine(hash, pair.Key, pair.Value));
}