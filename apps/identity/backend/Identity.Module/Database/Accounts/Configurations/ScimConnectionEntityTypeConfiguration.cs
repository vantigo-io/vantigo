using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class ScimConnectionEntityTypeConfiguration : IEntityTypeConfiguration<ScimConnection>
{
    public void Configure(EntityTypeBuilder<ScimConnection> builder)
    {
        builder.ToTable("scim_connections", "identity");
        builder.HasKey(item => item.Id).HasName("pk_scim_connections");
        builder.Property(item => item.Id).HasColumnName("id");
        builder.Property(item => item.FederationConnectionId).HasColumnName("federation_connection_id").IsRequired();
        builder.Property(item => item.Mode).HasColumnName("mode").HasConversion<string>().HasMaxLength(32).IsRequired();
        builder.Property(item => item.IsEnabled).HasColumnName("is_enabled").IsRequired();
        builder.Property(item => item.TokenVersion).HasColumnName("token_version").IsRequired();
        builder.Property(item => item.CreatedAt).HasColumnName("created_at").IsRequired();
        builder.Property(item => item.UpdatedAt).HasColumnName("updated_at").IsRequired();
        builder.Property(item => item.LastRotatedAt).HasColumnName("last_rotated_at");
        builder.Property(item => item.LastRevokedAt).HasColumnName("last_revoked_at");
        builder.Property(item => item.ConcurrencyStamp).HasColumnName("concurrency_stamp").IsConcurrencyToken().IsRequired();
        builder.HasOne<FederationConnection>().WithMany().HasForeignKey(item => item.FederationConnectionId)
            .HasConstraintName("fk_scim_connections_federation_connections_id").OnDelete(DeleteBehavior.Restrict);
        builder.HasIndex(item => item.FederationConnectionId).IsUnique().HasDatabaseName("ux_scim_connections_federation_connection_id");
    }
}