using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class FederatedIdentityEntityTypeConfiguration : IEntityTypeConfiguration<FederatedIdentity>
{
    public void Configure(EntityTypeBuilder<FederatedIdentity> builder)
    {
        builder.ToTable("federated_identities", "identity");
        builder.HasKey(identity => identity.Id).HasName("pk_federated_identities");
        builder.Property(identity => identity.Id).HasColumnName("id");
        builder.Property(identity => identity.ConnectionId).HasColumnName("connection_id").IsRequired();
        builder.Property(identity => identity.Issuer).HasColumnName("issuer").HasMaxLength(2048).IsRequired();
        builder.Property(identity => identity.Subject).HasColumnName("subject").HasMaxLength(512).IsRequired();
        builder.Property(identity => identity.DirectoryTenantId).HasColumnName("directory_tenant_id").HasMaxLength(128);
        builder.Property(identity => identity.DirectoryObjectId).HasColumnName("directory_object_id");
        builder.Property(identity => identity.UserId).HasColumnName("user_id").IsRequired();
        builder.Property(identity => identity.CreatedAt).HasColumnName("created_at").IsRequired();
        builder.Property(identity => identity.UpdatedAt).HasColumnName("updated_at").IsRequired();
        builder.HasOne<FederationConnection>()
            .WithMany()
            .HasForeignKey(identity => identity.ConnectionId)
            .HasConstraintName("fk_federated_identities_connections_connection_id")
            .OnDelete(DeleteBehavior.Restrict);
        builder.HasOne<ApplicationUser>()
            .WithMany()
            .HasForeignKey(identity => identity.UserId)
            .HasConstraintName("fk_federated_identities_users_user_id")
            .OnDelete(DeleteBehavior.Restrict);
        builder.HasIndex(identity => new { identity.ConnectionId, identity.Issuer, identity.Subject })
            .IsUnique().HasDatabaseName("ux_federated_identities_connection_issuer_subject");
        builder.HasIndex(identity => identity.UserId).HasDatabaseName("ix_federated_identities_user_id");
        builder.HasIndex(identity => new { identity.ConnectionId, identity.DirectoryTenantId, identity.DirectoryObjectId })
            .IsUnique().HasFilter("directory_tenant_id IS NOT NULL AND directory_object_id IS NOT NULL")
            .HasDatabaseName("ux_federated_identities_connection_directory_identity");
    }
}