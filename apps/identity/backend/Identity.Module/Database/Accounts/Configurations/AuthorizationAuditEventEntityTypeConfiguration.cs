using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class AuthorizationAuditEventEntityTypeConfiguration : IEntityTypeConfiguration<AuthorizationAuditEvent>
{
    public void Configure(EntityTypeBuilder<AuthorizationAuditEvent> builder)
    {
        builder.ToTable("authorization_audit_events", "identity");
        builder.HasKey(item => item.Id).HasName("pk_authorization_audit_events");
        builder.Property(item => item.Id).HasColumnName("id");
        builder.Property(item => item.ActorUserId).HasColumnName("actor_user_id");
        builder.Property(item => item.TargetUserId).HasColumnName("target_user_id");
        builder.Property(item => item.TargetRoleId).HasColumnName("target_role_id");
        builder.Property(item => item.Action).HasColumnName("action").HasMaxLength(100).IsRequired();
        builder.Property(item => item.Details).HasColumnName("details").HasMaxLength(4000).IsRequired();
        builder.Property(item => item.BeforeJson).HasColumnName("before_json").HasMaxLength(10000).IsRequired();
        builder.Property(item => item.AfterJson).HasColumnName("after_json").HasMaxLength(10000).IsRequired();
        builder.Property(item => item.CorrelationId).HasColumnName("correlation_id").HasMaxLength(200).IsRequired();
        builder.Property(item => item.MfaAuthenticated).HasColumnName("mfa_authenticated").IsRequired();
        builder.Property(item => item.OccurredAt).HasColumnName("occurred_at").IsRequired();
        builder.HasIndex(item => item.OccurredAt).HasDatabaseName("ix_authorization_audit_events_occurred_at");
    }
}