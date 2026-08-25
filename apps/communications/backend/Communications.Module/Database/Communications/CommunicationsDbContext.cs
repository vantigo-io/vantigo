using Microsoft.EntityFrameworkCore;

using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;
using Vantigo.Tenancy.EntityFramework;

namespace Vantigo.Communications.Database.Communications;

public sealed class CommunicationsDbContext(
    DbContextOptions<CommunicationsDbContext> options,
    ITenantContext? tenantContext = null) : DbContext(options), ITenantDbContext
{
    private readonly ITenantContext _tenantContext = tenantContext ?? UnresolvedTenantContext.Instance;

    /// <inheritdoc />
    public Guid CurrentTenantId => _tenantContext.Current.Value;
    internal DbSet<Channel> Channels => Set<Channel>();
    internal DbSet<ChannelCredential> ChannelCredentials => Set<ChannelCredential>();
    internal DbSet<Participant> Participants => Set<Participant>();
    internal DbSet<Conversation> Conversations => Set<Conversation>();
    internal DbSet<ConversationCustomerCandidate> ConversationCustomerCandidates => Set<ConversationCustomerCandidate>();
    internal DbSet<ConversationParticipant> ConversationParticipants => Set<ConversationParticipant>();
    internal DbSet<ConversationMessage> ConversationMessages => Set<ConversationMessage>();
    internal DbSet<MessageAttachment> MessageAttachments => Set<MessageAttachment>();
    internal DbSet<AttachmentUpload> AttachmentUploads => Set<AttachmentUpload>();
    internal DbSet<MessageDelivery> MessageDeliveries => Set<MessageDelivery>();
    internal DbSet<MessageEvent> MessageEvents => Set<MessageEvent>();
    internal DbSet<Tag> Tags => Set<Tag>();
    internal DbSet<ConversationTag> ConversationTags => Set<ConversationTag>();
    internal DbSet<ConversationReadState> ConversationReadStates => Set<ConversationReadState>();
    internal DbSet<Suppression> Suppressions => Set<Suppression>();
    internal DbSet<IdempotencyRecord> IdempotencyRecords => Set<IdempotencyRecord>();
    internal DbSet<InboundReceipt> InboundReceipts => Set<InboundReceipt>();
    internal DbSet<InboundEmailJob> InboundEmailJobs => Set<InboundEmailJob>();
    internal DbSet<AttachmentCleanupRecord> AttachmentCleanupRecords => Set<AttachmentCleanupRecord>();
    internal DbSet<OutboxJob> OutboxJobs => Set<OutboxJob>();
    internal DbSet<AiInteraction> AiInteractions => Set<AiInteraction>();

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        modelBuilder.HasDefaultSchema("communications");

        modelBuilder.Entity<Channel>(entity =>
        {
            entity.ToTable("channels");
            entity.HasKey(item => item.Id);
            entity.Property(item => item.Type).HasMaxLength(30).IsRequired();
            entity.Property(item => item.Address).HasMaxLength(320).IsRequired();
            entity.Property(item => item.DisplayName).HasMaxLength(200);
            entity.Property(item => item.Provider).HasMaxLength(20).IsRequired().HasDefaultValue("smtp");
            entity.Property(item => item.IsDefault).HasDefaultValue(false);
            entity.Property(item => item.IsActive).HasDefaultValue(true);
            entity.HasIndex(item => new { item.TenantId, item.Type, item.Address }).IsUnique();
            entity.HasIndex(item => new { item.TenantId, item.Type, item.IsDefault }).IsUnique().HasFilter("\"IsDefault\" = true");
        });
        modelBuilder.Entity<ChannelCredential>(entity =>
        {
            entity.ToTable("channel_credentials");
            entity.HasKey(item => item.Id);
            entity.Property(item => item.SettingsJson).HasColumnType("text").IsRequired();
            entity.Property(item => item.SecretCiphertext).HasColumnType("text").IsRequired();
            entity.HasOne(item => item.Channel).WithOne(item => item.Credential)
                .HasForeignKey<ChannelCredential>(item => item.ChannelId).OnDelete(DeleteBehavior.Cascade);
            entity.HasIndex(item => new { item.TenantId, item.ChannelId }).IsUnique();
        });
        modelBuilder.Entity<Participant>(entity =>
        {
            entity.ToTable("participants");
            entity.HasKey(item => item.Id);
            entity.Property(item => item.ChannelId).IsRequired();
            entity.Property(item => item.Address).HasMaxLength(320).IsRequired();
            entity.Property(item => item.DisplayName).HasMaxLength(200);
            entity.HasOne(item => item.Channel).WithMany(item => item.Participants).HasForeignKey(item => item.ChannelId).OnDelete(DeleteBehavior.Restrict);
            entity.HasIndex(item => new { item.TenantId, item.ChannelId, item.Address }).IsUnique();
            entity.HasIndex(item => new { item.TenantId, item.ContactId });
        });
        modelBuilder.Entity<Conversation>(entity =>
        {
            entity.ToTable("conversations");
            entity.HasKey(item => item.Id);
            entity.Property(item => item.Subject).HasMaxLength(998);
            entity.Property(item => item.Status).HasMaxLength(20).IsRequired();
            entity.Property(item => item.CustomerAssociationSource).HasMaxLength(20);
            entity.Property(item => item.SuggestedCustomerReasoning).HasColumnType("text");
            entity.Property(item => item.PreviewText).HasMaxLength(500);
            entity.HasOne(item => item.Channel).WithMany(item => item.Conversations)
                .HasForeignKey(item => item.ChannelId).OnDelete(DeleteBehavior.Restrict);
            entity.HasIndex(item => new { item.TenantId, item.Status, item.LastActivityAt }).IsDescending(false, true, true);
            entity.HasIndex(item => new { item.TenantId, item.CustomerId });
            entity.HasIndex(item => new { item.TenantId, item.SuggestedCustomerId });
            entity.ToTable(table => table.HasCheckConstraint("ck_conversations_customer_association_source", "\"CustomerAssociationSource\" IS NULL OR \"CustomerAssociationSource\" IN ('manual', 'automatic')"));
            entity.ToTable(table => table.HasCheckConstraint("ck_conversations_suggested_customer_confidence", "\"SuggestedCustomerConfidence\" IS NULL OR (\"SuggestedCustomerConfidence\" >= 0 AND \"SuggestedCustomerConfidence\" <= 1)"));
        });
        modelBuilder.Entity<ConversationCustomerCandidate>(entity =>
        {
            entity.ToTable("conversation_customer_candidates");
            entity.HasKey(item => new { item.ConversationId, item.CustomerId });
            entity.Property(item => item.CustomerId).IsRequired();
            entity.Property(item => item.CreatedAt).IsRequired();
            entity.HasOne(item => item.Conversation).WithMany(item => item.CustomerCandidates)
                .HasForeignKey(item => item.ConversationId).OnDelete(DeleteBehavior.Cascade);
            entity.HasIndex(item => new { item.TenantId, item.CustomerId });
        });
        modelBuilder.Entity<ConversationMessage>(entity =>
        {
            entity.ToTable("conversation_messages");
            entity.HasKey(item => item.Id);
            entity.Property(item => item.Direction).HasMaxLength(20).IsRequired();
            entity.Property(item => item.Subject).HasMaxLength(998);
            entity.Property(item => item.TextBody).HasColumnType("text");
            entity.Property(item => item.HtmlBody).HasColumnType("text");
            entity.Property(item => item.ChannelMetadataJson).HasColumnType("jsonb");
            entity.Property(item => item.RawPayloadStorageKey).HasMaxLength(1000);
            entity.Property(item => item.RfcMessageId).HasMaxLength(998);
            entity.HasOne(item => item.Conversation).WithMany(item => item.Messages)
                .HasForeignKey(item => item.ConversationId).OnDelete(DeleteBehavior.Cascade);
            entity.HasOne(item => item.Participant).WithMany(item => item.Messages)
                .HasForeignKey(item => item.ParticipantId).OnDelete(DeleteBehavior.SetNull);
            entity.HasIndex(item => new { item.TenantId, item.ConversationId, item.OccurredAt });
            entity.HasIndex(item => new { item.TenantId, item.RfcMessageId });
        });
        modelBuilder.Entity<ConversationParticipant>(entity =>
        {
            entity.ToTable("conversation_participants");
            entity.HasKey(item => new { item.ConversationId, item.ParticipantId });
            entity.Property(item => item.Role).HasMaxLength(30).IsRequired();
            entity.HasOne(item => item.Conversation).WithMany(item => item.Participants).HasForeignKey(item => item.ConversationId).OnDelete(DeleteBehavior.Cascade);
            entity.HasOne(item => item.Participant).WithMany(item => item.Conversations).HasForeignKey(item => item.ParticipantId).OnDelete(DeleteBehavior.Cascade);
            entity.HasIndex(item => new { item.TenantId, item.ParticipantId });
        });
        modelBuilder.Entity<MessageAttachment>(entity =>
        {
            entity.ToTable("message_attachments");
            entity.HasKey(item => item.Id);
            entity.Property(item => item.FileName).HasMaxLength(500).IsRequired();
            entity.Property(item => item.ContentType).HasMaxLength(200).IsRequired();
            entity.Property(item => item.ContentHash).HasMaxLength(128).IsRequired();
            entity.Property(item => item.ContentId).HasMaxLength(500);
            entity.Property(item => item.StorageKey).HasMaxLength(1000).IsRequired();
            entity.Property(item => item.ScanStatus).HasMaxLength(20).IsRequired();
            entity.Property(item => item.ScanLeaseId).HasMaxLength(100);
            entity.Property(item => item.ScanError).HasColumnType("text");
            entity.HasOne(item => item.Message).WithMany(item => item.Attachments)
                .HasForeignKey(item => item.MessageId).OnDelete(DeleteBehavior.Cascade);
            entity.HasIndex(item => new { item.TenantId, item.MessageId });
            entity.HasIndex(item => new { item.TenantId, item.ScanStatus, item.NextScanAt });
        });
        modelBuilder.Entity<AttachmentUpload>(entity =>
        {
            entity.ToTable("attachment_uploads");
            entity.HasKey(item => item.Id);
            entity.Property(item => item.FileName).HasMaxLength(500).IsRequired();
            entity.Property(item => item.ContentType).HasMaxLength(200).IsRequired();
            entity.Property(item => item.ContentHash).HasMaxLength(128).IsRequired();
            entity.Property(item => item.ContentId).HasMaxLength(500);
            entity.Property(item => item.StorageKey).HasMaxLength(1000).IsRequired();
            entity.Property(item => item.ScanStatus).HasMaxLength(20).IsRequired();
            entity.Property(item => item.ScanLeaseId).HasMaxLength(100);
            entity.Property(item => item.ScanError).HasColumnType("text");
            entity.HasOne(item => item.Conversation).WithMany().HasForeignKey(item => item.ConversationId).OnDelete(DeleteBehavior.Cascade);
            entity.HasIndex(item => new { item.TenantId, item.ScanStatus, item.NextScanAt });
            entity.HasIndex(item => new { item.TenantId, item.ConversationId, item.UploadedByUserId, item.ExpiresAt });
            entity.HasIndex(item => new { item.TenantId, item.UploadedByUserId, item.IdempotencyKey }).IsUnique();
        });
        modelBuilder.Entity<MessageDelivery>(entity =>
        {
            entity.ToTable("message_deliveries");
            entity.HasKey(item => item.Id);
            entity.Property(item => item.RecipientAddress).HasMaxLength(320).IsRequired();
            entity.Property(item => item.RecipientType).HasMaxLength(10).IsRequired();
            entity.Property(item => item.Status).HasMaxLength(40).IsRequired();
            entity.Property(item => item.LastError).HasColumnType("text");
            entity.HasOne(item => item.Message).WithMany(item => item.Deliveries)
                .HasForeignKey(item => item.MessageId).OnDelete(DeleteBehavior.Cascade);
            entity.HasOne(item => item.RecipientParticipant).WithMany().HasForeignKey(item => item.RecipientParticipantId).OnDelete(DeleteBehavior.SetNull);
            entity.HasIndex(item => new { item.TenantId, item.MessageId });
            entity.HasIndex(item => new { item.TenantId, item.RecipientParticipantId });
        });
        modelBuilder.Entity<MessageEvent>(entity =>
        {
            entity.ToTable("message_events");
            entity.HasKey(item => item.Id);
            entity.Property(item => item.EventType).HasMaxLength(60).IsRequired();
            entity.Property(item => item.DataJson).HasColumnType("text");
            entity.HasOne(item => item.Message).WithMany(item => item.Events)
                .HasForeignKey(item => item.MessageId).OnDelete(DeleteBehavior.Cascade);
            entity.HasOne(item => item.Delivery).WithMany(item => item.Events)
                .HasForeignKey(item => item.DeliveryId).OnDelete(DeleteBehavior.Restrict);
            entity.HasIndex(item => new { item.TenantId, item.MessageId, item.OccurredAt });
        });
        modelBuilder.Entity<Tag>(entity =>
        {
            entity.ToTable("tags");
            entity.HasKey(item => item.Id);
            entity.Property(item => item.Name).HasMaxLength(100).IsRequired();
            entity.Property(item => item.Color).HasMaxLength(20);
            entity.HasIndex(item => new { item.TenantId, item.Name }).IsUnique();
        });
        modelBuilder.Entity<ConversationTag>(entity =>
        {
            entity.ToTable("conversation_tags");
            entity.HasKey(item => new { item.ConversationId, item.TagId });
            entity.HasOne(item => item.Conversation).WithMany(item => item.Tags)
                .HasForeignKey(item => item.ConversationId).OnDelete(DeleteBehavior.Cascade);
            entity.HasOne(item => item.Tag).WithMany(item => item.Conversations)
                .HasForeignKey(item => item.TagId).OnDelete(DeleteBehavior.Cascade);
        });
        modelBuilder.Entity<ConversationReadState>(entity =>
        {
            entity.ToTable("conversation_read_states");
            entity.HasKey(item => new { item.ConversationId, item.UserId });
            entity.HasOne(item => item.Conversation).WithMany(item => item.ReadStates)
                .HasForeignKey(item => item.ConversationId).OnDelete(DeleteBehavior.Cascade);
        });
        modelBuilder.Entity<Suppression>(entity =>
        {
            entity.ToTable("suppressions");
            entity.HasKey(item => item.Id);
            entity.Property(item => item.NormalizedEmailAddress).HasMaxLength(320).IsRequired();
            entity.Property(item => item.Reason).HasMaxLength(500);
            entity.HasIndex(item => new { item.TenantId, item.NormalizedEmailAddress }).IsUnique();
        });
        modelBuilder.Entity<IdempotencyRecord>(entity =>
        {
            entity.ToTable("idempotency_records");
            entity.HasKey(item => item.Id);
            entity.Property(item => item.Key).HasMaxLength(200).IsRequired();
            entity.Property(item => item.PayloadFingerprint).HasMaxLength(64).IsRequired();
            entity.HasIndex(item => new { item.TenantId, item.ConversationId });
            entity.HasOne<Conversation>().WithMany().HasForeignKey(item => item.ConversationId).OnDelete(DeleteBehavior.Cascade);
            entity.HasIndex(item => new { item.TenantId, item.Key }).IsUnique();
            entity.HasIndex(item => new { item.TenantId, item.MessageId });
        });
        modelBuilder.Entity<InboundReceipt>(entity =>
        {
            entity.ToTable("inbound_receipts");
            entity.HasKey(item => item.Id);
            entity.Property(item => item.Provider).HasMaxLength(50).IsRequired();
            entity.Property(item => item.ProviderEventId).HasMaxLength(500).IsRequired();
            entity.Property(item => item.Status).HasMaxLength(30).IsRequired();
            entity.Property(item => item.RfcMessageId).HasMaxLength(998);
            entity.Property(item => item.PayloadHash).HasMaxLength(64);
            entity.HasOne(item => item.Channel).WithMany().HasForeignKey(item => item.ChannelId).OnDelete(DeleteBehavior.Cascade);
            entity.HasOne(item => item.ConversationMessage).WithMany().HasForeignKey(item => item.ConversationMessageId).OnDelete(DeleteBehavior.SetNull);
            entity.HasIndex(item => new { item.TenantId, item.ChannelId, item.Provider, item.ProviderEventId }).IsUnique();
            entity.HasIndex(item => new { item.TenantId, item.ChannelId, item.Provider, item.RfcMessageId }).IsUnique().HasFilter("\"RfcMessageId\" IS NOT NULL");
        });
        modelBuilder.Entity<InboundEmailJob>(entity =>
        {
            entity.ToTable("inbound_email_jobs");
            entity.HasKey(item => item.Id);
            entity.Property(item => item.RawMimeStorageKey).HasMaxLength(1000).IsRequired();
            entity.Property(item => item.Status).HasMaxLength(30).IsRequired();
            entity.Property(item => item.LeaseId).HasMaxLength(100);
            entity.Property(item => item.LastError).HasColumnType("text");
            entity.Property(item => item.EnvelopeSenderAddress).HasMaxLength(320);
            entity.Property(item => item.EnvelopeRecipientAddress).HasMaxLength(320);
            entity.HasOne(item => item.Channel).WithMany().HasForeignKey(item => item.ChannelId).OnDelete(DeleteBehavior.Cascade);
            entity.HasOne(item => item.InboundReceipt).WithOne(item => item.Job).HasForeignKey<InboundEmailJob>(item => item.InboundReceiptId).OnDelete(DeleteBehavior.Cascade);
            entity.HasIndex(item => new { item.TenantId, item.Status, item.NextAttemptAt });
            entity.HasIndex(item => new { item.TenantId, item.InboundReceiptId }).IsUnique();
        });
        modelBuilder.Entity<AttachmentCleanupRecord>(entity =>
        {
            entity.ToTable("attachment_cleanup_records");
            entity.HasKey(item => item.Id);
            entity.Property(item => item.StorageKey).HasMaxLength(1000).IsRequired();
            entity.Property(item => item.Status).HasMaxLength(30).IsRequired();
            entity.Property(item => item.LeaseId).HasMaxLength(100);
            entity.Property(item => item.ReservationExpiresAt);
            entity.Property(item => item.LastError).HasColumnType("text");
            entity.HasIndex(item => new { item.TenantId, item.Status, item.CreatedAt });
        });
        modelBuilder.Entity<OutboxJob>(entity =>
        {
            entity.ToTable("outbox_jobs");
            entity.HasKey(item => item.Id);
            entity.Property(item => item.Status).HasMaxLength(30).IsRequired();
            entity.Property(item => item.LeaseId).HasMaxLength(100);
            entity.Property(item => item.LastError).HasColumnType("text");
            entity.HasOne(item => item.Message).WithMany().HasForeignKey(item => item.MessageId).OnDelete(DeleteBehavior.Cascade);
            entity.HasIndex(item => new { item.TenantId, item.Status, item.NextAttemptAt });
        });
        modelBuilder.Entity<AiInteraction>(entity =>
        {
            entity.ToTable("ai_interactions");
            entity.HasKey(item => item.Id);
            entity.Property(item => item.Operation).HasMaxLength(50).IsRequired();
            entity.Property(item => item.Provider).HasMaxLength(50).IsRequired();
            entity.Property(item => item.Model).HasMaxLength(150).IsRequired();
            entity.Property(item => item.ContextDigest).HasMaxLength(64).IsRequired();
            entity.Property(item => item.ContextVersion).HasMaxLength(30).IsRequired();
            entity.Property(item => item.ResultSummary).HasMaxLength(200);
            entity.Property(item => item.ValidationSummary).HasMaxLength(500);
            entity.Property(item => item.ErrorSummary).HasMaxLength(200);
            entity.HasOne(item => item.Conversation).WithMany().HasForeignKey(item => item.ConversationId).OnDelete(DeleteBehavior.Cascade);
            entity.HasOne(item => item.Message).WithMany().HasForeignKey(item => item.MessageId).OnDelete(DeleteBehavior.SetNull);
            entity.HasIndex(item => new { item.TenantId, item.ConversationId, item.CreatedAt });
            entity.HasIndex(item => new { item.TenantId, item.Operation });
        });

        modelBuilder.ApplyTenantOwnership(this);
        foreach (var entityType in modelBuilder.Model.GetEntityTypes()
                     .Where(item => typeof(ITenantOwned).IsAssignableFrom(item.ClrType)))
        {
            modelBuilder.Entity(entityType.ClrType)
                .Property<Guid>(nameof(ITenantOwned.TenantId))
                .HasColumnName("tenant_id");
        }
    }

    private sealed class UnresolvedTenantContext : ITenantContext
    {
        internal static readonly UnresolvedTenantContext Instance = new();

        public bool IsResolved => false;

        public TenantId Current => throw new TenantUnresolvedException();
    }
}