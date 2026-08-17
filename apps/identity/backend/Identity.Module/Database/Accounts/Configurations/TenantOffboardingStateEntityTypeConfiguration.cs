using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class TenantOffboardingStateEntityTypeConfiguration : IEntityTypeConfiguration<TenantOffboardingState>
{
    public void Configure(EntityTypeBuilder<TenantOffboardingState> builder)
    {
        builder.ToTable("tenant_offboarding_states", "identity");
        builder.HasKey(state => state.TenantId).HasName("pk_tenant_offboarding_states");
        builder.Property(state => state.TenantId).HasColumnName("tenant_id");
        builder.Property(state => state.ExportId).HasColumnName("export_id").IsRequired();
        builder.Property(state => state.PurgeToken).HasColumnName("purge_token").HasMaxLength(128).IsRequired();
        builder.Property(state => state.RequestedAtUtc).HasColumnName("requested_at_utc").IsRequired();
        builder.Property(state => state.PurgeRequestedAtUtc).HasColumnName("purge_requested_at_utc");
        builder.HasOne<Tenant>()
            .WithOne()
            .HasForeignKey<TenantOffboardingState>(state => state.TenantId)
            .HasConstraintName("fk_tenant_offboarding_states_tenants_tenant_id")
            .OnDelete(DeleteBehavior.Cascade);
    }
}