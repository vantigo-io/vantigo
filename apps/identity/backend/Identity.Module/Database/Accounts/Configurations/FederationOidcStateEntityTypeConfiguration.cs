using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class FederationOidcStateEntityTypeConfiguration : IEntityTypeConfiguration<FederationOidcState>
{
    public void Configure(EntityTypeBuilder<FederationOidcState> builder)
    {
        builder.ToTable("federation_oidc_states", "identity");
        builder.HasKey(state => state.StateId).HasName("pk_federation_oidc_states");
        builder.Property(state => state.StateId).HasColumnName("state_id");
        builder.Property(state => state.StateHash).HasColumnName("state_hash").HasMaxLength(64).IsRequired();
        builder.Property(state => state.IssuedAt).HasColumnName("issued_at").IsRequired();
        builder.Property(state => state.ExpiresAt).HasColumnName("expires_at").IsRequired();
        builder.Property(state => state.ConsumedAt).HasColumnName("consumed_at");
        builder.HasIndex(state => state.StateHash).IsUnique().HasDatabaseName("ux_federation_oidc_states_hash");
        builder.HasIndex(state => state.ExpiresAt).HasDatabaseName("ix_federation_oidc_states_expires_at");
    }
}