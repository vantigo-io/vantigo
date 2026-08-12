using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class RoleMetadataEntityTypeConfiguration : IEntityTypeConfiguration<RoleMetadata>
{
    public void Configure(EntityTypeBuilder<RoleMetadata> builder)
    {
        builder.ToTable("role_metadata", "identity");
        builder.HasKey(item => item.RoleId).HasName("pk_role_metadata");
        builder.Property(item => item.RoleId).HasColumnName("role_id");
        builder.Property(item => item.DisplayName).HasColumnName("display_name").HasMaxLength(200).IsRequired();
        builder.Property(item => item.Description).HasColumnName("description").HasMaxLength(2000).IsRequired();
        builder.Property(item => item.IsSystem).HasColumnName("is_system").IsRequired();
        builder.Property(item => item.IsBuiltIn).HasColumnName("is_built_in").IsRequired();
        builder.Property(item => item.StewardUserId).HasColumnName("steward_user_id");
        builder.Property(item => item.ConcurrencyStamp).HasColumnName("concurrency_stamp").IsConcurrencyToken().IsRequired();
        builder.HasOne<Microsoft.AspNetCore.Identity.IdentityRole<Guid>>()
            .WithOne()
            .HasForeignKey<RoleMetadata>(item => item.RoleId)
            .HasConstraintName("fk_role_metadata_roles_role_id")
            .OnDelete(DeleteBehavior.Cascade);
        builder.HasIndex(item => item.StewardUserId).HasDatabaseName("ix_role_metadata_steward_user_id");
    }
}