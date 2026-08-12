using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class RolePermissionEntityTypeConfiguration : IEntityTypeConfiguration<RolePermission>
{
    public void Configure(EntityTypeBuilder<RolePermission> builder)
    {
        builder.ToTable("role_permissions", "identity");
        builder.HasKey(item => new { item.RoleId, item.PermissionKey }).HasName("pk_role_permissions");
        builder.Property(item => item.RoleId).HasColumnName("role_id");
        builder.Property(item => item.PermissionKey).HasColumnName("permission_key").HasMaxLength(200).IsRequired();
        builder.HasOne<Microsoft.AspNetCore.Identity.IdentityRole<Guid>>()
            .WithMany()
            .HasForeignKey(item => item.RoleId)
            .HasConstraintName("fk_role_permissions_roles_role_id")
            .OnDelete(DeleteBehavior.Cascade);
        builder.HasIndex(item => item.PermissionKey).HasDatabaseName("ix_role_permissions_permission_key");
    }
}