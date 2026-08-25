using Microsoft.EntityFrameworkCore.Migrations;

using Vantigo.Tenancy.EntityFramework;

#nullable disable

namespace Vantigo.Communications.Database.Communications.Migrations
{
    /// <inheritdoc />
    public partial class TenantRlsPolicyNullSafe : Migration
    {
        private static readonly string[] Tables =
        [
            "ai_interactions",
            "attachment_cleanup_records",
            "attachment_uploads",
            "channel_credentials",
            "channels",
            "conversation_customer_candidates",
            "conversation_messages",
            "conversation_participants",
            "conversation_read_states",
            "conversation_tags",
            "conversations",
            "idempotency_records",
            "inbound_email_jobs",
            "inbound_receipts",
            "message_attachments",
            "message_deliveries",
            "message_events",
            "outbox_jobs",
            "participants",
            "suppressions",
            "tags",
        ];

        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            foreach (var table in Tables)
                migrationBuilder.MakeTenantRlsPolicyNullSafe("communications", table);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            foreach (var table in Tables)
                migrationBuilder.RevertTenantRlsPolicyNullSafe("communications", table);
        }
    }
}