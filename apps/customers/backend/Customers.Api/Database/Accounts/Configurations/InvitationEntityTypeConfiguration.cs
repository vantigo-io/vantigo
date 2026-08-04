using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Customers.Api.Database.Accounts;

namespace Vantigo.Customers.Api.Database.Accounts.Configurations;

internal sealed class InvitationEntityTypeConfiguration : IEntityTypeConfiguration<Invitation>
{
    public void Configure(EntityTypeBuilder<Invitation> builder)
    {
        builder.ToTable("invitations", "accounts");
        builder.HasKey(invitation => invitation.Id).HasName("pk_invitations");
        builder.Property(invitation => invitation.Id).HasColumnName("id");
        builder.Property(invitation => invitation.Email).HasColumnName("email").HasMaxLength(256).IsRequired();
        builder.Property(invitation => invitation.NormalizedEmail).HasColumnName("normalized_email").HasMaxLength(256).IsRequired();
        builder.Property(invitation => invitation.Role).HasColumnName("role").HasMaxLength(32).IsRequired();
        builder.Property(invitation => invitation.DisplayName).HasColumnName("display_name").HasMaxLength(200);
        builder.Property(invitation => invitation.TokenHash).HasColumnName("token_hash").HasMaxLength(64).IsRequired();
        builder.Property(invitation => invitation.CreatedAt).HasColumnName("created_at").IsRequired();
        builder.Property(invitation => invitation.ExpiresAt).HasColumnName("expires_at").IsRequired();
        builder.Property(invitation => invitation.RevokedAt).HasColumnName("revoked_at");
        builder.Property(invitation => invitation.AcceptedAt).HasColumnName("accepted_at");
        builder.Property(invitation => invitation.InvitedByUserId).HasColumnName("invited_by_user_id").IsRequired();
        builder.HasIndex(invitation => invitation.TokenHash).IsUnique().HasDatabaseName("ux_invitations_token_hash");
        builder.HasIndex(invitation => invitation.NormalizedEmail)
            .HasDatabaseName("ux_invitations_active_normalized_email")
            .HasFilter("accepted_at IS NULL AND revoked_at IS NULL")
            .IsUnique();
    }
}
