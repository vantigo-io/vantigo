using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class AccessGroupRoleMappingEntityTypeConfiguration : IEntityTypeConfiguration<AccessGroupRoleMapping>
{
    public void Configure(EntityTypeBuilder<AccessGroupRoleMapping> builder)
    {
        builder.ToTable("access_group_role_mappings", "identity");
        builder.HasKey(mapping => new { mapping.GroupId, mapping.RoleId }).HasName("pk_access_group_role_mappings");
        builder.Property(mapping => mapping.GroupId).HasColumnName("group_id");
        builder.Property(mapping => mapping.RoleId).HasColumnName("role_id");
        builder.Property(mapping => mapping.TenantId).HasColumnName("tenant_id");
        builder.Property(mapping => mapping.Source).HasColumnName("source").HasConversion<string>().HasMaxLength(20).IsRequired();
        builder.Property(mapping => mapping.CreatedAt).HasColumnName("created_at").IsRequired();
        builder.HasOne<AccessGroup>().WithMany().HasForeignKey(mapping => mapping.GroupId).HasConstraintName("fk_access_group_role_mappings_access_groups_group_id").OnDelete(DeleteBehavior.Cascade);
        builder.HasOne<Microsoft.AspNetCore.Identity.IdentityRole<Guid>>().WithMany().HasForeignKey(mapping => mapping.RoleId).HasConstraintName("fk_access_group_role_mappings_roles_role_id").OnDelete(DeleteBehavior.Cascade);
        builder.HasIndex(mapping => mapping.RoleId).HasDatabaseName("ix_access_group_role_mappings_role_id");
        builder.HasIndex(mapping => new { mapping.GroupId, mapping.RoleId, mapping.TenantId })
            .IsUnique().HasDatabaseName("ux_access_group_role_mappings_group_role_tenant");
    }
}