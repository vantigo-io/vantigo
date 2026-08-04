using System;

using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Communications.Api.Database.Communications.Migrations
{
    /// <inheritdoc />
    public partial class InitialCommunications : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.EnsureSchema(
                name: "communications");

            migrationBuilder.CreateTable(
                name: "idempotency_records",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    Key = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false),
                    PayloadFingerprint = table.Column<string>(type: "character varying(64)", maxLength: 64, nullable: false),
                    MessageId = table.Column<Guid>(type: "uuid", nullable: false),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_idempotency_records", x => x.Id);
                });

            migrationBuilder.CreateTable(
                name: "shared_mailboxes",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    FromAddress = table.Column<string>(type: "character varying(320)", maxLength: 320, nullable: false),
                    DisplayName = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: true),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    IsActive = table.Column<bool>(type: "boolean", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_shared_mailboxes", x => x.Id);
                });

            migrationBuilder.CreateTable(
                name: "suppressions",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    NormalizedEmailAddress = table.Column<string>(type: "character varying(320)", maxLength: 320, nullable: false),
                    Reason = table.Column<string>(type: "character varying(500)", maxLength: 500, nullable: true),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_suppressions", x => x.Id);
                });

            migrationBuilder.CreateTable(
                name: "email_messages",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    MailboxId = table.Column<Guid>(type: "uuid", nullable: false),
                    Subject = table.Column<string>(type: "character varying(998)", maxLength: 998, nullable: false),
                    TextBody = table.Column<string>(type: "text", nullable: true),
                    HtmlBody = table.Column<string>(type: "text", nullable: true),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    CreatedByUserId = table.Column<Guid>(type: "uuid", nullable: true),
                    Source = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_email_messages", x => x.Id);
                    table.ForeignKey(
                        name: "FK_email_messages_shared_mailboxes_MailboxId",
                        column: x => x.MailboxId,
                        principalSchema: "communications",
                        principalTable: "shared_mailboxes",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Restrict);
                });

            migrationBuilder.CreateTable(
                name: "external_entity_links",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    MessageId = table.Column<Guid>(type: "uuid", nullable: false),
                    SourceSystem = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: false),
                    SourceInstance = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false),
                    EntityType = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: false),
                    ExternalEntityId = table.Column<string>(type: "character varying(500)", maxLength: 500, nullable: false),
                    DisplayLabel = table.Column<string>(type: "character varying(500)", maxLength: 500, nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_external_entity_links", x => x.Id);
                    table.ForeignKey(
                        name: "FK_external_entity_links_email_messages_MessageId",
                        column: x => x.MessageId,
                        principalSchema: "communications",
                        principalTable: "email_messages",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "outbox_jobs",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    MessageId = table.Column<Guid>(type: "uuid", nullable: false),
                    Status = table.Column<string>(type: "character varying(30)", maxLength: 30, nullable: false),
                    Attempts = table.Column<int>(type: "integer", nullable: false),
                    NextAttemptAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    LeaseId = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: true),
                    LeaseUntil = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    CompletedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    LastError = table.Column<string>(type: "text", nullable: true),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_outbox_jobs", x => x.Id);
                    table.ForeignKey(
                        name: "FK_outbox_jobs_email_messages_MessageId",
                        column: x => x.MessageId,
                        principalSchema: "communications",
                        principalTable: "email_messages",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "recipient_deliveries",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    MessageId = table.Column<Guid>(type: "uuid", nullable: false),
                    EmailAddress = table.Column<string>(type: "character varying(320)", maxLength: 320, nullable: false),
                    RecipientType = table.Column<string>(type: "character varying(10)", maxLength: 10, nullable: false),
                    Status = table.Column<string>(type: "character varying(40)", maxLength: 40, nullable: false),
                    Attempts = table.Column<int>(type: "integer", nullable: false),
                    LastError = table.Column<string>(type: "text", nullable: true),
                    AcceptedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_recipient_deliveries", x => x.Id);
                    table.ForeignKey(
                        name: "FK_recipient_deliveries_email_messages_MessageId",
                        column: x => x.MessageId,
                        principalSchema: "communications",
                        principalTable: "email_messages",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "message_events",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    MessageId = table.Column<Guid>(type: "uuid", nullable: false),
                    DeliveryId = table.Column<Guid>(type: "uuid", nullable: true),
                    EventType = table.Column<string>(type: "character varying(60)", maxLength: 60, nullable: false),
                    OccurredAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    DataJson = table.Column<string>(type: "text", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_message_events", x => x.Id);
                    table.ForeignKey(
                        name: "FK_message_events_email_messages_MessageId",
                        column: x => x.MessageId,
                        principalSchema: "communications",
                        principalTable: "email_messages",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                    table.ForeignKey(
                        name: "FK_message_events_recipient_deliveries_DeliveryId",
                        column: x => x.DeliveryId,
                        principalSchema: "communications",
                        principalTable: "recipient_deliveries",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Restrict);
                });

            migrationBuilder.CreateIndex(
                name: "IX_email_messages_CreatedAt",
                schema: "communications",
                table: "email_messages",
                column: "CreatedAt");

            migrationBuilder.CreateIndex(
                name: "IX_email_messages_MailboxId",
                schema: "communications",
                table: "email_messages",
                column: "MailboxId");

            migrationBuilder.CreateIndex(
                name: "IX_external_entity_links_MessageId",
                schema: "communications",
                table: "external_entity_links",
                column: "MessageId");

            migrationBuilder.CreateIndex(
                name: "IX_external_entity_links_SourceSystem_SourceInstance_EntityTyp~",
                schema: "communications",
                table: "external_entity_links",
                columns: new[] { "SourceSystem", "SourceInstance", "EntityType", "ExternalEntityId" });

            migrationBuilder.CreateIndex(
                name: "IX_idempotency_records_Key",
                schema: "communications",
                table: "idempotency_records",
                column: "Key",
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_idempotency_records_MessageId",
                schema: "communications",
                table: "idempotency_records",
                column: "MessageId");

            migrationBuilder.CreateIndex(
                name: "IX_message_events_DeliveryId",
                schema: "communications",
                table: "message_events",
                column: "DeliveryId");

            migrationBuilder.CreateIndex(
                name: "IX_message_events_MessageId_OccurredAt",
                schema: "communications",
                table: "message_events",
                columns: new[] { "MessageId", "OccurredAt" });

            migrationBuilder.CreateIndex(
                name: "IX_outbox_jobs_MessageId",
                schema: "communications",
                table: "outbox_jobs",
                column: "MessageId");

            migrationBuilder.CreateIndex(
                name: "IX_outbox_jobs_Status_NextAttemptAt",
                schema: "communications",
                table: "outbox_jobs",
                columns: new[] { "Status", "NextAttemptAt" });

            migrationBuilder.CreateIndex(
                name: "IX_recipient_deliveries_MessageId",
                schema: "communications",
                table: "recipient_deliveries",
                column: "MessageId");

            migrationBuilder.CreateIndex(
                name: "IX_shared_mailboxes_FromAddress",
                schema: "communications",
                table: "shared_mailboxes",
                column: "FromAddress",
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_suppressions_NormalizedEmailAddress",
                schema: "communications",
                table: "suppressions",
                column: "NormalizedEmailAddress",
                unique: true);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "external_entity_links",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "idempotency_records",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "message_events",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "outbox_jobs",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "suppressions",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "recipient_deliveries",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "email_messages",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "shared_mailboxes",
                schema: "communications");
        }
    }
}