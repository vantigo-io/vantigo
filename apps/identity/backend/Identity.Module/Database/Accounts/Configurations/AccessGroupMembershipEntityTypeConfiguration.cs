using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class AccessGroupMembershipEntityTypeConfiguration : IEntityTypeConfiguration<AccessGroupMembership>
{
    public void Configure(EntityTypeBuilder<AccessGroupMembership> builder)
    {
        builder.ToTable("access_group_memberships", "identity");
        builder.HasKey(membership => new { membership.GroupId, membership.UserId }).HasName("pk_access_group_memberships");
        builder.Property(membership => membership.GroupId).HasColumnName("group_id");
        builder.Property(membership => membership.UserId).HasColumnName("user_id");
        builder.Property(membership => membership.TenantId).HasColumnName("tenant_id");
        builder.Property(membership => membership.Source).HasColumnName("source").HasConversion<string>().HasMaxLength(20).IsRequired();
        builder.Property(membership => membership.IsUpstreamPresent).HasColumnName("is_upstream_present").IsRequired();
        builder.Property(membership => membership.Override).HasColumnName("membership_override").HasConversion<string>().HasMaxLength(32);
        builder.Property(membership => membership.UpdatedAt).HasColumnName("updated_at").IsRequired();
        builder.HasOne<AccessGroup>().WithMany().HasForeignKey(membership => membership.GroupId).HasConstraintName("fk_access_group_memberships_access_groups_group_id").OnDelete(DeleteBehavior.Cascade);
        builder.HasOne<ApplicationUser>().WithMany().HasForeignKey(membership => membership.UserId).HasConstraintName("fk_access_group_memberships_users_user_id").OnDelete(DeleteBehavior.Cascade);
        builder.HasIndex(membership => membership.UserId).HasDatabaseName("ix_access_group_memberships_user_id");
        builder.HasIndex(membership => new { membership.GroupId, membership.UserId, membership.TenantId })
            .IsUnique().HasDatabaseName("ux_access_group_memberships_group_user_tenant");
    }
}