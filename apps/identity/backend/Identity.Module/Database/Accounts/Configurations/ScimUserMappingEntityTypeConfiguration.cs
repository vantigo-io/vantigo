using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class ScimUserMappingEntityTypeConfiguration : IEntityTypeConfiguration<ScimUserMapping>
{
    public void Configure(EntityTypeBuilder<ScimUserMapping> builder)
    {
        builder.ToTable("scim_user_mappings", "identity");
        builder.HasKey(item => item.Id).HasName("pk_scim_user_mappings");
        builder.Property(item => item.Id).HasColumnName("id");
        builder.Property(item => item.ScimConnectionId).HasColumnName("scim_connection_id").IsRequired();
        builder.Property(item => item.UserId).HasColumnName("user_id").IsRequired();
        builder.Property(item => item.ResourceId).HasColumnName("resource_id").HasMaxLength(256).IsRequired();
        builder.Property(item => item.ExternalId).HasColumnName("external_id").HasMaxLength(512).IsRequired();
        builder.Property(item => item.UserName).HasColumnName("user_name").HasMaxLength(512).IsRequired();
        builder.Property(item => item.UpstreamActive).HasColumnName("upstream_active").IsRequired();
        builder.Property(item => item.SourceProfileJson).HasColumnName("source_profile_json").HasMaxLength(20000);
        builder.Property(item => item.LastSynchronizedAt).HasColumnName("last_synchronized_at").IsRequired();
        builder.Property(item => item.Version).HasColumnName("version").IsRequired();
        builder.Property(item => item.ETag).HasColumnName("etag").HasMaxLength(128).IsConcurrencyToken().IsRequired();
        builder.Property(item => item.CreatedAt).HasColumnName("created_at").IsRequired();
        builder.Property(item => item.UpdatedAt).HasColumnName("updated_at").IsRequired();
        builder.HasOne<ScimConnection>().WithMany().HasForeignKey(item => item.ScimConnectionId)
            .HasConstraintName("fk_scim_user_mappings_connections_id").OnDelete(DeleteBehavior.Restrict);
        builder.HasOne<ApplicationUser>().WithMany().HasForeignKey(item => item.UserId)
            .HasConstraintName("fk_scim_user_mappings_users_id").OnDelete(DeleteBehavior.Restrict);
        builder.HasIndex(item => new { item.ScimConnectionId, item.ResourceId }).IsUnique().HasDatabaseName("ux_scim_user_mappings_resource");
        builder.HasIndex(item => new { item.ScimConnectionId, item.ExternalId }).IsUnique().HasDatabaseName("ux_scim_user_mappings_external_id");
        builder.HasIndex(item => new { item.ScimConnectionId, item.UserId }).IsUnique().HasDatabaseName("ux_scim_user_mappings_user");
    }
}