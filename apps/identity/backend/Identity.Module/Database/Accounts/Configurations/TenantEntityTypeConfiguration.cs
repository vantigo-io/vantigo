using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class TenantEntityTypeConfiguration : IEntityTypeConfiguration<Tenant>
{
    public void Configure(EntityTypeBuilder<Tenant> builder)
    {
        builder.ToTable("tenants", "identity");
        builder.HasKey(tenant => tenant.Id).HasName("pk_tenants");
        builder.Property(tenant => tenant.Id).HasColumnName("id");
        builder.Property(tenant => tenant.Name).HasColumnName("name").HasMaxLength(200).IsRequired();
        builder.Property(tenant => tenant.Slug).HasColumnName("slug").HasMaxLength(63).IsRequired();
        builder.Property(tenant => tenant.Status).HasColumnName("status").HasConversion<string>().HasMaxLength(16).IsRequired();
        builder.Property(tenant => tenant.EnabledModules).HasColumnName("enabled_modules").HasColumnType("text[]").IsRequired();
        builder.Property(tenant => tenant.CreatedAtUtc).HasColumnName("created_at_utc").IsRequired();
        builder.HasIndex(tenant => tenant.Slug).IsUnique().HasDatabaseName("ux_tenants_slug");
    }
}

internal sealed class TenantMembershipEntityTypeConfiguration : IEntityTypeConfiguration<TenantMembership>
{
    public void Configure(EntityTypeBuilder<TenantMembership> builder)
    {
        builder.ToTable("tenant_memberships", "identity");
        builder.HasKey(membership => new { membership.UserId, membership.TenantId }).HasName("pk_tenant_memberships");
        builder.Property(membership => membership.UserId).HasColumnName("user_id");
        builder.Property(membership => membership.TenantId).HasColumnName("tenant_id");
        builder.Property(membership => membership.CreatedAtUtc).HasColumnName("created_at_utc").IsRequired();
        builder.HasOne<ApplicationUser>().WithMany().HasForeignKey(membership => membership.UserId)
            .HasConstraintName("fk_tenant_memberships_users_user_id").OnDelete(DeleteBehavior.Cascade);
        builder.HasOne<Tenant>().WithMany().HasForeignKey(membership => membership.TenantId)
            .HasConstraintName("fk_tenant_memberships_tenants_tenant_id").OnDelete(DeleteBehavior.Cascade);
        builder.HasIndex(membership => membership.TenantId).HasDatabaseName("ix_tenant_memberships_tenant_id");
    }
}

internal sealed class TenantSsoConfigurationEntityTypeConfiguration : IEntityTypeConfiguration<TenantSsoConfiguration>
{
    public void Configure(EntityTypeBuilder<TenantSsoConfiguration> builder)
    {
        builder.ToTable("tenant_sso_configurations", "identity");
        builder.HasKey(configuration => configuration.TenantId).HasName("pk_tenant_sso_configurations");
        builder.Property(configuration => configuration.TenantId).HasColumnName("tenant_id");
        builder.Property(configuration => configuration.EntraTenantId).HasColumnName("entra_tenant_id").IsRequired();
        builder.Property(configuration => configuration.AllowedEmailDomain).HasColumnName("allowed_email_domain").HasMaxLength(255);
        builder.Property(configuration => configuration.JitProvisioningEnabled).HasColumnName("jit_provisioning_enabled").IsRequired();
        builder.HasOne<Tenant>().WithMany().HasForeignKey(configuration => configuration.TenantId)
            .HasConstraintName("fk_tenant_sso_configurations_tenants_tenant_id").OnDelete(DeleteBehavior.Cascade);
    }
}