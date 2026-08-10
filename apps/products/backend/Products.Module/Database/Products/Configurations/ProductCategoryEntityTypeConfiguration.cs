using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Products.Domain.Products;

namespace Vantigo.Products.Database.Products.Configurations;

internal sealed class ProductCategoryEntityTypeConfiguration : IEntityTypeConfiguration<ProductCategory>
{
    public void Configure(EntityTypeBuilder<ProductCategory> builder)
    {
        builder.ToTable("product_categories");

        builder.HasKey(c => c.Id);

        builder.Property(c => c.Id)
            .HasColumnName("id")
            .HasComment("The unique identifier of the category")
            .IsRequired()
            .HasIdentityOptions(1001, 1);

        builder.Property(c => c.Name)
            .HasColumnName("name")
            .HasComment("The display name of the category, unique among its siblings")
            .HasMaxLength(ProductCategory.NameMaxLength)
            .IsUnicode(true)
            .IsRequired();

        builder.Property(c => c.ParentId)
            .HasColumnName("parent_id")
            .HasComment("The parent category; null means the category is a root");

        builder.HasOne<ProductCategory>()
            .WithMany()
            .HasForeignKey(c => c.ParentId)
            .OnDelete(DeleteBehavior.Restrict);

        // NULLS NOT DISTINCT so root categories (parent_id IS NULL) also get
        // sibling-unique names.
        builder.HasIndex(c => new { c.ParentId, c.Name })
            .IsUnique()
            .AreNullsDistinct(false);
    }
}