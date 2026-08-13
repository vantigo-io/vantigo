using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class ScimBearerTokenEntityTypeConfiguration : IEntityTypeConfiguration<ScimBearerToken>
{
    public void Configure(EntityTypeBuilder<ScimBearerToken> builder)
    {
        builder.ToTable("scim_bearer_tokens", "identity");
        builder.HasKey(item => item.Id).HasName("pk_scim_bearer_tokens");
        builder.Property(item => item.Id).HasColumnName("id");
        builder.Property(item => item.ScimConnectionId).HasColumnName("scim_connection_id").IsRequired();
        builder.Property(item => item.Version).HasColumnName("version").IsRequired();
        builder.Property(item => item.TokenHash).HasColumnName("token_hash").HasMaxLength(64).IsRequired();
        builder.Property(item => item.CreatedAt).HasColumnName("created_at").IsRequired();
        builder.Property(item => item.ExpiresAt).HasColumnName("expires_at");
        builder.Property(item => item.RevokedAt).HasColumnName("revoked_at");
        builder.Property(item => item.IsCurrent).HasColumnName("is_current").IsRequired();
        builder.HasOne<ScimConnection>().WithMany().HasForeignKey(item => item.ScimConnectionId)
            .HasConstraintName("fk_scim_bearer_tokens_connections_id").OnDelete(DeleteBehavior.Restrict);
        builder.HasIndex(item => new { item.ScimConnectionId, item.Version }).IsUnique().HasDatabaseName("ux_scim_bearer_tokens_connection_version");
        builder.HasIndex(item => item.TokenHash).IsUnique().HasDatabaseName("ux_scim_bearer_tokens_hash");
    }
}