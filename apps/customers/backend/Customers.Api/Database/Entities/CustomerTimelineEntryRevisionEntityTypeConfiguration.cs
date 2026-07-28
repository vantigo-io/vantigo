using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Customers.Api.Domain.Timeline;

namespace Vantigo.Customers.Api.Database.Entities;

internal sealed class CustomerTimelineEntryRevisionEntityTypeConfiguration :
    IEntityTypeConfiguration<CustomerTimelineEntryRevision>
{
    public void Configure(EntityTypeBuilder<CustomerTimelineEntryRevision> builder)
    {
        builder.ToTable("customers_timeline_entries_revisions");
        builder.HasKey(revision => revision.Id);

        builder.Property(revision => revision.Id)
            .HasColumnName("id")
            .HasIdentityOptions(1001, 1)
            .IsRequired();
        builder.Property(revision => revision.CustomerTimelineEntryId)
            .HasColumnName("customer_timeline_entry_id")
            .IsRequired();
        builder.Property(revision => revision.RevisionNumber).HasColumnName("revision_number").IsRequired();
        builder.Property(revision => revision.Provenance).HasColumnName("provenance").HasMaxLength(20).IsRequired();
        builder.Property(revision => revision.Producer).HasColumnName("producer").HasMaxLength(100).IsRequired();
        builder.Property(revision => revision.EventType).HasColumnName("event_type").HasMaxLength(100).IsRequired();
        builder.Property(revision => revision.OccurredOn).HasColumnName("occurred_on").IsRequired();
        builder.Property(revision => revision.OccurredAt).HasColumnName("occurred_at");
        builder.Property(revision => revision.Summary).HasColumnName("summary").HasMaxLength(500);
        builder.Property(revision => revision.Note).HasColumnName("note").HasMaxLength(10000);
        builder.Property(revision => revision.SourceUrl).HasColumnName("source_url").HasMaxLength(2048);
        builder.Property(revision => revision.PayloadJson).HasColumnName("payload_json").HasColumnType("jsonb");
        builder.Property(revision => revision.PayloadVersion).HasColumnName("payload_version").IsRequired();
        builder.Property(revision => revision.State).HasColumnName("state").HasMaxLength(20).IsRequired();
        builder.Property(revision => revision.ActorKind).HasColumnName("actor_kind").HasMaxLength(30).IsRequired();
        builder.Property(revision => revision.ActorDisplay).HasColumnName("actor_display").HasMaxLength(255);
        builder.Property(revision => revision.CreatedAt).HasColumnName("created_at").IsRequired();
        builder.Property(revision => revision.DeletedAt).HasColumnName("deleted_at");

        builder.HasOne(revision => revision.Entry)
            .WithMany(entry => entry.Revisions)
            .HasForeignKey(revision => revision.CustomerTimelineEntryId)
            .OnDelete(DeleteBehavior.Cascade);

        builder.HasIndex(revision => new { revision.CustomerTimelineEntryId, revision.RevisionNumber })
            .IsUnique()
            .HasDatabaseName("ux_customers_timeline_entries_revisions_entry_revision");
    }
}
