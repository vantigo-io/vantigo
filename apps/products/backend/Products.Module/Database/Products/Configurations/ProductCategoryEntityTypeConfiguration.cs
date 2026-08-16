using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Products.Domain.Products;

namespace Vantigo.Products.Database.Products.Configurations;

internal sealed class ProductCategoryEntityTypeConfiguration : IEntityTypeConfiguration<ProductCategory>
{
    public void Configure(EntityTypeBuilder<ProductCategory> builder)
    {
        builder.ToTable("product_categories");

        builder.HasKey(c => new { c.TenantId, c.Id });

        builder.Property(c => c.Id)
            .HasColumnName("id")
            .HasComment("The unique identifier of the category")
            .IsRequired()
            .ValueGeneratedOnAdd()
            .HasIdentityOptions(1001, 1);

        builder.Property(c => c.TenantId)
            .HasColumnName("tenant_id")
            .HasComment("The tenant that owns the category")
            .IsRequired();

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
            .HasForeignKey(c => new { c.TenantId, c.ParentId })
            .HasPrincipalKey(parent => new { parent.TenantId, parent.Id })
            .OnDelete(DeleteBehavior.Restrict);

        // NULLS NOT DISTINCT so root categories (parent_id IS NULL) also get
        // sibling-unique names.
        builder.HasIndex(c => new { c.TenantId, c.ParentId, c.Name })
            .IsUnique()
            .AreNullsDistinct(false);

        builder.HasIndex(c => new { c.TenantId, c.ParentId });
    }
}