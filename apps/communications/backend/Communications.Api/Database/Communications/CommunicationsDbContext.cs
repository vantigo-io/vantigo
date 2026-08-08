using Microsoft.EntityFrameworkCore;

namespace Vantigo.Communications.Api.Database.Communications;

public sealed class CommunicationsDbContext(DbContextOptions<CommunicationsDbContext> options) : DbContext(options)
{
    public DbSet<SharedMailbox> SharedMailboxes => Set<SharedMailbox>();
    public DbSet<MailboxProviderCredential> MailboxProviderCredentials => Set<MailboxProviderCredential>();
    public DbSet<EmailMessage> EmailMessages => Set<EmailMessage>();
    public DbSet<RecipientDelivery> RecipientDeliveries => Set<RecipientDelivery>();
    public DbSet<MessageEvent> MessageEvents => Set<MessageEvent>();
    public DbSet<ExternalEntityLink> ExternalEntityLinks => Set<ExternalEntityLink>();
    public DbSet<Suppression> Suppressions => Set<Suppression>();
    public DbSet<IdempotencyRecord> IdempotencyRecords => Set<IdempotencyRecord>();
    public DbSet<OutboxJob> OutboxJobs => Set<OutboxJob>();

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        modelBuilder.HasDefaultSchema("communications");
        modelBuilder.Entity<SharedMailbox>(entity =>
        {
            entity.ToTable("shared_mailboxes");
            entity.HasKey(mailbox => mailbox.Id);
            entity.Property(mailbox => mailbox.FromAddress).HasMaxLength(320).IsRequired();
            entity.Property(mailbox => mailbox.DisplayName).HasMaxLength(200);
            entity.Property(mailbox => mailbox.Provider).HasMaxLength(20).IsRequired().HasDefaultValue("smtp");
            entity.Property(mailbox => mailbox.IsDefault).HasDefaultValue(false);
            entity.HasIndex(mailbox => mailbox.FromAddress).IsUnique();
        });
        modelBuilder.Entity<MailboxProviderCredential>(entity =>
        {
            entity.ToTable("mailbox_provider_credentials");
            entity.HasKey(credential => credential.Id);
            entity.Property(credential => credential.Provider).HasMaxLength(20).IsRequired();
            entity.Property(credential => credential.SettingsJson).HasColumnType("text").IsRequired();
            entity.Property(credential => credential.SecretCiphertext).HasColumnType("text").IsRequired();
            entity.HasOne(credential => credential.Mailbox).WithOne(mailbox => mailbox.Credential)
                .HasForeignKey<MailboxProviderCredential>(credential => credential.MailboxId).OnDelete(DeleteBehavior.Cascade);
            entity.HasIndex(credential => credential.MailboxId).IsUnique();
        });
        modelBuilder.Entity<EmailMessage>(entity =>
        {
            entity.ToTable("email_messages");
            entity.HasKey(message => message.Id);
            entity.Property(message => message.Subject).HasMaxLength(998).IsRequired();
            entity.Property(message => message.TextBody).HasColumnType("text");
            entity.Property(message => message.HtmlBody).HasColumnType("text");
            entity.Property(message => message.Source).HasMaxLength(100);
            entity.HasOne(message => message.Mailbox).WithMany().HasForeignKey(message => message.MailboxId).OnDelete(DeleteBehavior.Restrict);
            entity.HasIndex(message => message.CreatedAt);
            entity.HasIndex(message => message.ArchivedAt);
        });
        modelBuilder.Entity<RecipientDelivery>(entity =>
        {
            entity.ToTable("recipient_deliveries");
            entity.HasKey(delivery => delivery.Id);
            entity.Property(delivery => delivery.EmailAddress).HasMaxLength(320).IsRequired();
            entity.Property(delivery => delivery.RecipientType).HasMaxLength(10).IsRequired();
            entity.Property(delivery => delivery.Status).HasMaxLength(40).IsRequired();
            entity.Property(delivery => delivery.LastError).HasColumnType("text");
            entity.HasOne(delivery => delivery.Message).WithMany(message => message.Deliveries).HasForeignKey(delivery => delivery.MessageId).OnDelete(DeleteBehavior.Cascade);
            entity.HasIndex(delivery => delivery.MessageId);
        });
        modelBuilder.Entity<MessageEvent>(entity =>
        {
            entity.ToTable("message_events");
            entity.HasKey(messageEvent => messageEvent.Id);
            entity.Property(messageEvent => messageEvent.EventType).HasMaxLength(60).IsRequired();
            entity.Property(messageEvent => messageEvent.DataJson).HasColumnType("text");
            entity.HasOne(messageEvent => messageEvent.Message).WithMany(message => message.Events).HasForeignKey(messageEvent => messageEvent.MessageId).OnDelete(DeleteBehavior.Cascade);
            entity.HasOne(messageEvent => messageEvent.Delivery).WithMany(delivery => delivery.Events).HasForeignKey(messageEvent => messageEvent.DeliveryId).OnDelete(DeleteBehavior.Restrict);
            entity.HasIndex(messageEvent => new { messageEvent.MessageId, messageEvent.OccurredAt });
        });
        modelBuilder.Entity<ExternalEntityLink>(entity =>
        {
            entity.ToTable("external_entity_links");
            entity.HasKey(link => link.Id);
            entity.Property(link => link.SourceSystem).HasMaxLength(100).IsRequired();
            entity.Property(link => link.SourceInstance).HasMaxLength(200).IsRequired();
            entity.Property(link => link.EntityType).HasMaxLength(100).IsRequired();
            entity.Property(link => link.ExternalEntityId).HasMaxLength(500).IsRequired();
            entity.Property(link => link.DisplayLabel).HasMaxLength(500);
            entity.HasOne(link => link.Message).WithMany(message => message.ExternalLinks).HasForeignKey(link => link.MessageId).OnDelete(DeleteBehavior.Cascade);
            entity.HasIndex(link => new { link.SourceSystem, link.SourceInstance, link.EntityType, link.ExternalEntityId });
        });
        modelBuilder.Entity<Suppression>(entity =>
        {
            entity.ToTable("suppressions");
            entity.HasKey(suppression => suppression.Id);
            entity.Property(suppression => suppression.NormalizedEmailAddress).HasMaxLength(320).IsRequired();
            entity.Property(suppression => suppression.Reason).HasMaxLength(500);
            entity.HasIndex(suppression => suppression.NormalizedEmailAddress).IsUnique();
        });
        modelBuilder.Entity<IdempotencyRecord>(entity =>
        {
            entity.ToTable("idempotency_records");
            entity.HasKey(record => record.Id);
            entity.Property(record => record.Key).HasMaxLength(200).IsRequired();
            entity.Property(record => record.PayloadFingerprint).HasMaxLength(64).IsRequired();
            entity.HasIndex(record => record.Key).IsUnique();
            entity.HasIndex(record => record.MessageId);
        });
        modelBuilder.Entity<OutboxJob>(entity =>
        {
            entity.ToTable("outbox_jobs");
            entity.HasKey(job => job.Id);
            entity.Property(job => job.Status).HasMaxLength(30).IsRequired();
            entity.Property(job => job.LeaseId).HasMaxLength(100);
            entity.Property(job => job.LastError).HasColumnType("text");
            entity.HasOne(job => job.Message).WithMany().HasForeignKey(job => job.MessageId).OnDelete(DeleteBehavior.Cascade);
            entity.HasIndex(job => new { job.Status, job.NextAttemptAt });
        });
    }
}