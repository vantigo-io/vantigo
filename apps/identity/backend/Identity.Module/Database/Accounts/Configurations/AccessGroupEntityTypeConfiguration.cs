using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class AccessGroupEntityTypeConfiguration : IEntityTypeConfiguration<AccessGroup>
{
    public void Configure(EntityTypeBuilder<AccessGroup> builder)
    {
        builder.ToTable("access_groups", "identity");
        builder.HasKey(group => group.Id).HasName("pk_access_groups");
        builder.Property(group => group.Id).HasColumnName("id");
        builder.Property(group => group.ScimConnectionId).HasColumnName("scim_connection_id");
        builder.Property(group => group.DisplayName).HasColumnName("display_name").HasMaxLength(200).IsRequired();
        builder.Property(group => group.Source).HasColumnName("source").HasConversion<string>().HasMaxLength(20).IsRequired();
        builder.Property(group => group.ExternalId).HasColumnName("external_id").HasMaxLength(256);
        builder.Property(group => group.IsActive).HasColumnName("is_active").IsRequired();
        builder.Property(group => group.CreatedAt).HasColumnName("created_at").IsRequired();
        builder.Property(group => group.UpdatedAt).HasColumnName("updated_at").IsRequired();
        builder.Property(group => group.ConcurrencyStamp).HasColumnName("concurrency_stamp").IsConcurrencyToken().IsRequired();
        builder.HasIndex(group => group.DisplayName).IsUnique().HasDatabaseName("ux_access_groups_display_name");
        builder.HasOne<ScimConnection>().WithMany().HasForeignKey(group => group.ScimConnectionId)
            .HasConstraintName("fk_access_groups_scim_connections_id").OnDelete(DeleteBehavior.Restrict);
        builder.HasIndex(group => new { group.ScimConnectionId, group.ExternalId }).IsUnique()
            .HasFilter("external_id IS NOT NULL").HasDatabaseName("ux_access_groups_scim_external_id");
    }
}