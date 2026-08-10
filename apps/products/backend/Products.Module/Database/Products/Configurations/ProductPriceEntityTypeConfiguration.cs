using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Products.Domain.Products;

namespace Vantigo.Products.Database.Products.Configurations;

internal sealed class ProductPriceEntityTypeConfiguration : IEntityTypeConfiguration<ProductPrice>
{
    public void Configure(EntityTypeBuilder<ProductPrice> builder)
    {
        builder.ToTable("product_prices");

        builder.HasKey(p => p.Id);

        builder.Property(p => p.Id)
            .HasColumnName("id")
            .HasComment("The unique identifier of the price")
            .IsRequired()
            .HasIdentityOptions(1001, 1);

        builder.Property(p => p.VariantId)
            .HasColumnName("variant_id")
            .HasComment("The product variant the price belongs to")
            .IsRequired();

        builder.Property(p => p.Currency)
            .HasColumnName("currency")
            .HasComment("The ISO 4217 currency code of the price")
            .HasMaxLength(ProductPrice.CurrencyLength)
            .IsFixedLength()
            .IsUnicode(false)
            .IsRequired();

        builder.Property(p => p.Amount)
            .HasColumnName("amount")
            .HasComment("The price amount in the given currency, excluding VAT")
            .HasPrecision(12, 2)
            .IsRequired();

        builder.Property(p => p.ValidFrom)
            .HasColumnName("valid_from")
            .HasComment("When the price becomes valid; null means valid from the beginning of time");

        builder.Property(p => p.ValidTo)
            .HasColumnName("valid_to")
            .HasComment("When the price stops being valid (exclusive); null means open-ended");

        builder.HasIndex(p => new { p.VariantId, p.Currency });

        builder.Ignore(p => p.IsBounded);
    }
}