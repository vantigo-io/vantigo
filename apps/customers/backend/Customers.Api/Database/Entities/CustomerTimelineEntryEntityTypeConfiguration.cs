using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Customers.Api.Domain.Timeline;

namespace Vantigo.Customers.Api.Database.Entities;

internal sealed class CustomerTimelineEntryEntityTypeConfiguration :
    IEntityTypeConfiguration<CustomerTimelineEntry>
{
    public void Configure(EntityTypeBuilder<CustomerTimelineEntry> builder)
    {
        builder.ToTable("customers_timeline_entries");
        builder.HasKey(entry => entry.Id);

        builder.Property(entry => entry.Id)
            .HasColumnName("id")
            .HasIdentityOptions(1001, 1)
            .IsRequired();
        builder.Property(entry => entry.CustomerId).HasColumnName("customer_id").IsRequired();
        builder.Property(entry => entry.Provenance).HasColumnName("provenance").HasMaxLength(20).IsRequired();
        builder.Property(entry => entry.Producer).HasColumnName("producer").HasMaxLength(100).IsRequired();
        builder.Property(entry => entry.EventType).HasColumnName("event_type").HasMaxLength(100).IsRequired();
        builder.Property(entry => entry.OccurredOn).HasColumnName("occurred_on").IsRequired();
        builder.Property(entry => entry.OccurredAt).HasColumnName("occurred_at");
        builder.Property(entry => entry.Summary).HasColumnName("summary").HasMaxLength(500);
        builder.Property(entry => entry.Note).HasColumnName("note").HasMaxLength(10000);
        builder.Property(entry => entry.SourceUrl).HasColumnName("source_url").HasMaxLength(2048);
        builder.Property(entry => entry.PayloadJson).HasColumnName("payload_json").HasColumnType("jsonb");
        builder.Property(entry => entry.PayloadVersion).HasColumnName("payload_version").IsRequired();
        builder.Property(entry => entry.CurrentRevision)
            .HasColumnName("current_revision")
            .IsRequired()
            // The revision is both the public expectedRevision value and the
            // optimistic concurrency token for the current snapshot.
            .IsConcurrencyToken();
        builder.Property(entry => entry.State).HasColumnName("state").HasMaxLength(20).IsRequired();
        builder.Property(entry => entry.ActorKind).HasColumnName("actor_kind").HasMaxLength(30).IsRequired();
        builder.Property(entry => entry.ActorDisplay).HasColumnName("actor_display").HasMaxLength(255);
        builder.Property(entry => entry.CreatedAt).HasColumnName("created_at").IsRequired();
        builder.Property(entry => entry.UpdatedAt).HasColumnName("updated_at").IsRequired();
        builder.Property(entry => entry.DeletedAt).HasColumnName("deleted_at");

        builder.HasOne(entry => entry.Customer)
            .WithMany()
            .HasForeignKey(entry => entry.CustomerId)
            .OnDelete(DeleteBehavior.Cascade);

        // The feed index is created as a PostgreSQL expression index in the migration.
        // Its occurred_at IS NOT NULL key matches the LINQ order below, which puts
        // instants before date-only entries (the opposite of PostgreSQL DESC's default
        // NULL ordering).
    }
}
