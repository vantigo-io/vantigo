using System;

using Microsoft.EntityFrameworkCore.Migrations;

using Vantigo.Tenancy.EntityFramework;

#nullable disable

namespace Vantigo.Communications.Database.Communications.Migrations
{
    /// <inheritdoc />
    public partial class Initial : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.EnsureSchema(
                name: "communications");

            migrationBuilder.CreateTable(
                name: "attachment_cleanup_records",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    MessageId = table.Column<Guid>(type: "uuid", nullable: true),
                    StorageKey = table.Column<string>(type: "character varying(1000)", maxLength: 1000, nullable: false),
                    Status = table.Column<string>(type: "character varying(30)", maxLength: 30, nullable: false),
                    Attempts = table.Column<int>(type: "integer", nullable: false),
                    NextAttemptAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    LeaseId = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: true),
                    LeaseUntil = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    ReservationExpiresAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    LastError = table.Column<string>(type: "text", nullable: true),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_attachment_cleanup_records", x => x.Id);
                });

            migrationBuilder.CreateTable(
                name: "channels",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    Type = table.Column<string>(type: "character varying(30)", maxLength: 30, nullable: false),
                    Address = table.Column<string>(type: "character varying(320)", maxLength: 320, nullable: false),
                    DisplayName = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: true),
                    Provider = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: false, defaultValue: "smtp"),
                    IsDefault = table.Column<bool>(type: "boolean", nullable: false, defaultValue: false),
                    IsActive = table.Column<bool>(type: "boolean", nullable: false, defaultValue: true),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_channels", x => x.Id);
                });

            migrationBuilder.CreateTable(
                name: "suppressions",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    NormalizedEmailAddress = table.Column<string>(type: "character varying(320)", maxLength: 320, nullable: false),
                    Reason = table.Column<string>(type: "character varying(500)", maxLength: 500, nullable: true),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_suppressions", x => x.Id);
                });

            migrationBuilder.CreateTable(
                name: "tags",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    Name = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: false),
                    Color = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_tags", x => x.Id);
                });

            migrationBuilder.CreateTable(
                name: "channel_credentials",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    ChannelId = table.Column<Guid>(type: "uuid", nullable: false),
                    SettingsJson = table.Column<string>(type: "text", nullable: false),
                    SecretCiphertext = table.Column<string>(type: "text", nullable: false),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_channel_credentials", x => x.Id);
                    table.ForeignKey(
                        name: "FK_channel_credentials_channels_ChannelId",
                        column: x => x.ChannelId,
                        principalSchema: "communications",
                        principalTable: "channels",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "conversations",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    ChannelId = table.Column<Guid>(type: "uuid", nullable: false),
                    Subject = table.Column<string>(type: "character varying(998)", maxLength: 998, nullable: true),
                    Status = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: false),
                    AssignedUserId = table.Column<Guid>(type: "uuid", nullable: true),
                    CustomerId = table.Column<int>(type: "integer", nullable: true),
                    CustomerAssociationSource = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: true),
                    SuggestedCustomerId = table.Column<int>(type: "integer", nullable: true),
                    SuggestedCustomerConfidence = table.Column<double>(type: "double precision", nullable: true),
                    SuggestedCustomerReasoning = table.Column<string>(type: "text", nullable: true),
                    LastActivityAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    PreviewText = table.Column<string>(type: "character varying(500)", maxLength: 500, nullable: true),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_conversations", x => x.Id);
                    table.CheckConstraint("ck_conversations_customer_association_source", "\"CustomerAssociationSource\" IS NULL OR \"CustomerAssociationSource\" IN ('manual', 'automatic')");
                    table.CheckConstraint("ck_conversations_suggested_customer_confidence", "\"SuggestedCustomerConfidence\" IS NULL OR (\"SuggestedCustomerConfidence\" >= 0 AND \"SuggestedCustomerConfidence\" <= 1)");
                    table.ForeignKey(
                        name: "FK_conversations_channels_ChannelId",
                        column: x => x.ChannelId,
                        principalSchema: "communications",
                        principalTable: "channels",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Restrict);
                });

            migrationBuilder.CreateTable(
                name: "participants",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    ChannelId = table.Column<Guid>(type: "uuid", nullable: false),
                    Address = table.Column<string>(type: "character varying(320)", maxLength: 320, nullable: false),
                    DisplayName = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: true),
                    ContactId = table.Column<int>(type: "integer", nullable: true),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_participants", x => x.Id);
                    table.ForeignKey(
                        name: "FK_participants_channels_ChannelId",
                        column: x => x.ChannelId,
                        principalSchema: "communications",
                        principalTable: "channels",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Restrict);
                });

            migrationBuilder.CreateTable(
                name: "attachment_uploads",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    ConversationId = table.Column<Guid>(type: "uuid", nullable: false),
                    UploadedByUserId = table.Column<Guid>(type: "uuid", nullable: false),
                    IdempotencyKey = table.Column<string>(type: "text", nullable: false),
                    FileName = table.Column<string>(type: "character varying(500)", maxLength: 500, nullable: false),
                    ContentType = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false),
                    SizeBytes = table.Column<long>(type: "bigint", nullable: false),
                    ContentHash = table.Column<string>(type: "character varying(128)", maxLength: 128, nullable: false),
                    ContentId = table.Column<string>(type: "character varying(500)", maxLength: 500, nullable: true),
                    StorageKey = table.Column<string>(type: "character varying(1000)", maxLength: 1000, nullable: false),
                    ScanStatus = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: false),
                    ScanAttempts = table.Column<int>(type: "integer", nullable: false),
                    NextScanAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    ScanLeaseId = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: true),
                    ScanLeaseUntil = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    ScanError = table.Column<string>(type: "text", nullable: true),
                    IsInline = table.Column<bool>(type: "boolean", nullable: false),
                    ExpiresAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_attachment_uploads", x => x.Id);
                    table.ForeignKey(
                        name: "FK_attachment_uploads_conversations_ConversationId",
                        column: x => x.ConversationId,
                        principalSchema: "communications",
                        principalTable: "conversations",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "conversation_customer_candidates",
                schema: "communications",
                columns: table => new
                {
                    ConversationId = table.Column<Guid>(type: "uuid", nullable: false),
                    CustomerId = table.Column<int>(type: "integer", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_conversation_customer_candidates", x => new { x.ConversationId, x.CustomerId });
                    table.ForeignKey(
                        name: "FK_conversation_customer_candidates_conversations_Conversation~",
                        column: x => x.ConversationId,
                        principalSchema: "communications",
                        principalTable: "conversations",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "conversation_read_states",
                schema: "communications",
                columns: table => new
                {
                    ConversationId = table.Column<Guid>(type: "uuid", nullable: false),
                    UserId = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    LastReadAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_conversation_read_states", x => new { x.ConversationId, x.UserId });
                    table.ForeignKey(
                        name: "FK_conversation_read_states_conversations_ConversationId",
                        column: x => x.ConversationId,
                        principalSchema: "communications",
                        principalTable: "conversations",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "conversation_tags",
                schema: "communications",
                columns: table => new
                {
                    ConversationId = table.Column<Guid>(type: "uuid", nullable: false),
                    TagId = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_conversation_tags", x => new { x.ConversationId, x.TagId });
                    table.ForeignKey(
                        name: "FK_conversation_tags_conversations_ConversationId",
                        column: x => x.ConversationId,
                        principalSchema: "communications",
                        principalTable: "conversations",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                    table.ForeignKey(
                        name: "FK_conversation_tags_tags_TagId",
                        column: x => x.TagId,
                        principalSchema: "communications",
                        principalTable: "tags",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "idempotency_records",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    Key = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false),
                    PayloadFingerprint = table.Column<string>(type: "character varying(64)", maxLength: 64, nullable: false),
                    ConversationId = table.Column<Guid>(type: "uuid", nullable: false),
                    MessageId = table.Column<Guid>(type: "uuid", nullable: false),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_idempotency_records", x => x.Id);
                    table.ForeignKey(
                        name: "FK_idempotency_records_conversations_ConversationId",
                        column: x => x.ConversationId,
                        principalSchema: "communications",
                        principalTable: "conversations",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "conversation_messages",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    ConversationId = table.Column<Guid>(type: "uuid", nullable: false),
                    Direction = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: false),
                    ParticipantId = table.Column<Guid>(type: "uuid", nullable: true),
                    AuthorUserId = table.Column<Guid>(type: "uuid", nullable: true),
                    Subject = table.Column<string>(type: "character varying(998)", maxLength: 998, nullable: true),
                    TextBody = table.Column<string>(type: "text", nullable: true),
                    HtmlBody = table.Column<string>(type: "text", nullable: true),
                    ChannelMetadataJson = table.Column<string>(type: "jsonb", nullable: true),
                    RawPayloadStorageKey = table.Column<string>(type: "character varying(1000)", maxLength: 1000, nullable: true),
                    OccurredAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    RfcMessageId = table.Column<string>(type: "character varying(998)", maxLength: 998, nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_conversation_messages", x => x.Id);
                    table.ForeignKey(
                        name: "FK_conversation_messages_conversations_ConversationId",
                        column: x => x.ConversationId,
                        principalSchema: "communications",
                        principalTable: "conversations",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                    table.ForeignKey(
                        name: "FK_conversation_messages_participants_ParticipantId",
                        column: x => x.ParticipantId,
                        principalSchema: "communications",
                        principalTable: "participants",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.SetNull);
                });

            migrationBuilder.CreateTable(
                name: "conversation_participants",
                schema: "communications",
                columns: table => new
                {
                    ConversationId = table.Column<Guid>(type: "uuid", nullable: false),
                    ParticipantId = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    Role = table.Column<string>(type: "character varying(30)", maxLength: 30, nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_conversation_participants", x => new { x.ConversationId, x.ParticipantId });
                    table.ForeignKey(
                        name: "FK_conversation_participants_conversations_ConversationId",
                        column: x => x.ConversationId,
                        principalSchema: "communications",
                        principalTable: "conversations",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                    table.ForeignKey(
                        name: "FK_conversation_participants_participants_ParticipantId",
                        column: x => x.ParticipantId,
                        principalSchema: "communications",
                        principalTable: "participants",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "ai_interactions",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    ConversationId = table.Column<Guid>(type: "uuid", nullable: false),
                    MessageId = table.Column<Guid>(type: "uuid", nullable: true),
                    Operation = table.Column<string>(type: "character varying(50)", maxLength: 50, nullable: false),
                    RequesterUserId = table.Column<Guid>(type: "uuid", nullable: true),
                    Provider = table.Column<string>(type: "character varying(50)", maxLength: 50, nullable: false),
                    Model = table.Column<string>(type: "character varying(150)", maxLength: 150, nullable: false),
                    ContextDigest = table.Column<string>(type: "character varying(64)", maxLength: 64, nullable: false),
                    ContextVersion = table.Column<string>(type: "character varying(30)", maxLength: 30, nullable: false),
                    ResultSummary = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: true),
                    ValidationSummary = table.Column<string>(type: "character varying(500)", maxLength: 500, nullable: true),
                    ErrorSummary = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: true),
                    DurationMs = table.Column<long>(type: "bigint", nullable: true),
                    InputTokenCount = table.Column<int>(type: "integer", nullable: true),
                    OutputTokenCount = table.Column<int>(type: "integer", nullable: true),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_ai_interactions", x => x.Id);
                    table.ForeignKey(
                        name: "FK_ai_interactions_conversation_messages_MessageId",
                        column: x => x.MessageId,
                        principalSchema: "communications",
                        principalTable: "conversation_messages",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.SetNull);
                    table.ForeignKey(
                        name: "FK_ai_interactions_conversations_ConversationId",
                        column: x => x.ConversationId,
                        principalSchema: "communications",
                        principalTable: "conversations",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "inbound_receipts",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    ChannelId = table.Column<Guid>(type: "uuid", nullable: false),
                    Provider = table.Column<string>(type: "character varying(50)", maxLength: 50, nullable: false),
                    ProviderEventId = table.Column<string>(type: "character varying(500)", maxLength: 500, nullable: false),
                    Status = table.Column<string>(type: "character varying(30)", maxLength: 30, nullable: false),
                    RfcMessageId = table.Column<string>(type: "character varying(998)", maxLength: 998, nullable: true),
                    PayloadHash = table.Column<string>(type: "character varying(64)", maxLength: 64, nullable: true),
                    ConversationMessageId = table.Column<Guid>(type: "uuid", nullable: true),
                    ReceivedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    ReservationExpiresAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_inbound_receipts", x => x.Id);
                    table.ForeignKey(
                        name: "FK_inbound_receipts_channels_ChannelId",
                        column: x => x.ChannelId,
                        principalSchema: "communications",
                        principalTable: "channels",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                    table.ForeignKey(
                        name: "FK_inbound_receipts_conversation_messages_ConversationMessageId",
                        column: x => x.ConversationMessageId,
                        principalSchema: "communications",
                        principalTable: "conversation_messages",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.SetNull);
                });

            migrationBuilder.CreateTable(
                name: "message_attachments",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    MessageId = table.Column<Guid>(type: "uuid", nullable: false),
                    FileName = table.Column<string>(type: "character varying(500)", maxLength: 500, nullable: false),
                    ContentType = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false),
                    SizeBytes = table.Column<long>(type: "bigint", nullable: false),
                    ContentHash = table.Column<string>(type: "character varying(128)", maxLength: 128, nullable: false),
                    ContentId = table.Column<string>(type: "character varying(500)", maxLength: 500, nullable: true),
                    StorageKey = table.Column<string>(type: "character varying(1000)", maxLength: 1000, nullable: false),
                    ScanStatus = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: false),
                    ScanAttempts = table.Column<int>(type: "integer", nullable: false),
                    NextScanAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    ScanLeaseId = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: true),
                    ScanLeaseUntil = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    ScanError = table.Column<string>(type: "text", nullable: true),
                    IsInline = table.Column<bool>(type: "boolean", nullable: false),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_message_attachments", x => x.Id);
                    table.ForeignKey(
                        name: "FK_message_attachments_conversation_messages_MessageId",
                        column: x => x.MessageId,
                        principalSchema: "communications",
                        principalTable: "conversation_messages",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "message_deliveries",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    MessageId = table.Column<Guid>(type: "uuid", nullable: false),
                    RecipientAddress = table.Column<string>(type: "character varying(320)", maxLength: 320, nullable: false),
                    RecipientType = table.Column<string>(type: "character varying(10)", maxLength: 10, nullable: false),
                    RecipientParticipantId = table.Column<Guid>(type: "uuid", nullable: true),
                    Status = table.Column<string>(type: "character varying(40)", maxLength: 40, nullable: false),
                    Attempts = table.Column<int>(type: "integer", nullable: false),
                    LastError = table.Column<string>(type: "text", nullable: true),
                    AcceptedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_message_deliveries", x => x.Id);
                    table.ForeignKey(
                        name: "FK_message_deliveries_conversation_messages_MessageId",
                        column: x => x.MessageId,
                        principalSchema: "communications",
                        principalTable: "conversation_messages",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                    table.ForeignKey(
                        name: "FK_message_deliveries_participants_RecipientParticipantId",
                        column: x => x.RecipientParticipantId,
                        principalSchema: "communications",
                        principalTable: "participants",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.SetNull);
                });

            migrationBuilder.CreateTable(
                name: "outbox_jobs",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
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
                        name: "FK_outbox_jobs_conversation_messages_MessageId",
                        column: x => x.MessageId,
                        principalSchema: "communications",
                        principalTable: "conversation_messages",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "inbound_email_jobs",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    ChannelId = table.Column<Guid>(type: "uuid", nullable: false),
                    InboundReceiptId = table.Column<Guid>(type: "uuid", nullable: false),
                    RawMimeStorageKey = table.Column<string>(type: "character varying(1000)", maxLength: 1000, nullable: false),
                    Status = table.Column<string>(type: "character varying(30)", maxLength: 30, nullable: false),
                    Attempts = table.Column<int>(type: "integer", nullable: false),
                    NextAttemptAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    LeaseId = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: true),
                    LeaseUntil = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    CompletedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    LastError = table.Column<string>(type: "text", nullable: true),
                    EnvelopeSenderAddress = table.Column<string>(type: "character varying(320)", maxLength: 320, nullable: true),
                    EnvelopeRecipientAddress = table.Column<string>(type: "character varying(320)", maxLength: 320, nullable: true),
                    IsSynthetic = table.Column<bool>(type: "boolean", nullable: false),
                    ReceivedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_inbound_email_jobs", x => x.Id);
                    table.ForeignKey(
                        name: "FK_inbound_email_jobs_channels_ChannelId",
                        column: x => x.ChannelId,
                        principalSchema: "communications",
                        principalTable: "channels",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                    table.ForeignKey(
                        name: "FK_inbound_email_jobs_inbound_receipts_InboundReceiptId",
                        column: x => x.InboundReceiptId,
                        principalSchema: "communications",
                        principalTable: "inbound_receipts",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "message_events",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
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
                        name: "FK_message_events_conversation_messages_MessageId",
                        column: x => x.MessageId,
                        principalSchema: "communications",
                        principalTable: "conversation_messages",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                    table.ForeignKey(
                        name: "FK_message_events_message_deliveries_DeliveryId",
                        column: x => x.DeliveryId,
                        principalSchema: "communications",
                        principalTable: "message_deliveries",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Restrict);
                });

            migrationBuilder.CreateIndex(
                name: "IX_ai_interactions_ConversationId",
                schema: "communications",
                table: "ai_interactions",
                column: "ConversationId");

            migrationBuilder.CreateIndex(
                name: "IX_ai_interactions_MessageId",
                schema: "communications",
                table: "ai_interactions",
                column: "MessageId");

            migrationBuilder.CreateIndex(
                name: "IX_ai_interactions_tenant_id_ConversationId_CreatedAt",
                schema: "communications",
                table: "ai_interactions",
                columns: new[] { "tenant_id", "ConversationId", "CreatedAt" });

            migrationBuilder.CreateIndex(
                name: "IX_ai_interactions_tenant_id_Operation",
                schema: "communications",
                table: "ai_interactions",
                columns: new[] { "tenant_id", "Operation" });

            migrationBuilder.CreateIndex(
                name: "IX_attachment_cleanup_records_tenant_id_Status_CreatedAt",
                schema: "communications",
                table: "attachment_cleanup_records",
                columns: new[] { "tenant_id", "Status", "CreatedAt" });

            migrationBuilder.CreateIndex(
                name: "IX_attachment_uploads_ConversationId",
                schema: "communications",
                table: "attachment_uploads",
                column: "ConversationId");

            migrationBuilder.CreateIndex(
                name: "IX_attachment_uploads_tenant_id_ConversationId_UploadedByUserI~",
                schema: "communications",
                table: "attachment_uploads",
                columns: new[] { "tenant_id", "ConversationId", "UploadedByUserId", "ExpiresAt" });

            migrationBuilder.CreateIndex(
                name: "IX_attachment_uploads_tenant_id_ScanStatus_NextScanAt",
                schema: "communications",
                table: "attachment_uploads",
                columns: new[] { "tenant_id", "ScanStatus", "NextScanAt" });

            migrationBuilder.CreateIndex(
                name: "IX_attachment_uploads_tenant_id_UploadedByUserId_IdempotencyKey",
                schema: "communications",
                table: "attachment_uploads",
                columns: new[] { "tenant_id", "UploadedByUserId", "IdempotencyKey" },
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_channel_credentials_ChannelId",
                schema: "communications",
                table: "channel_credentials",
                column: "ChannelId",
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_channel_credentials_tenant_id_ChannelId",
                schema: "communications",
                table: "channel_credentials",
                columns: new[] { "tenant_id", "ChannelId" },
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_channels_tenant_id_Type_Address",
                schema: "communications",
                table: "channels",
                columns: new[] { "tenant_id", "Type", "Address" },
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_channels_tenant_id_Type_IsDefault",
                schema: "communications",
                table: "channels",
                columns: new[] { "tenant_id", "Type", "IsDefault" },
                unique: true,
                filter: "\"IsDefault\" = true");

            migrationBuilder.CreateIndex(
                name: "IX_conversation_customer_candidates_tenant_id_CustomerId",
                schema: "communications",
                table: "conversation_customer_candidates",
                columns: new[] { "tenant_id", "CustomerId" });

            migrationBuilder.CreateIndex(
                name: "IX_conversation_messages_ConversationId",
                schema: "communications",
                table: "conversation_messages",
                column: "ConversationId");

            migrationBuilder.CreateIndex(
                name: "IX_conversation_messages_ParticipantId",
                schema: "communications",
                table: "conversation_messages",
                column: "ParticipantId");

            migrationBuilder.CreateIndex(
                name: "IX_conversation_messages_tenant_id_ConversationId_OccurredAt",
                schema: "communications",
                table: "conversation_messages",
                columns: new[] { "tenant_id", "ConversationId", "OccurredAt" });

            migrationBuilder.CreateIndex(
                name: "IX_conversation_messages_tenant_id_RfcMessageId",
                schema: "communications",
                table: "conversation_messages",
                columns: new[] { "tenant_id", "RfcMessageId" });

            migrationBuilder.CreateIndex(
                name: "IX_conversation_participants_ParticipantId",
                schema: "communications",
                table: "conversation_participants",
                column: "ParticipantId");

            migrationBuilder.CreateIndex(
                name: "IX_conversation_participants_tenant_id_ParticipantId",
                schema: "communications",
                table: "conversation_participants",
                columns: new[] { "tenant_id", "ParticipantId" });

            migrationBuilder.CreateIndex(
                name: "IX_conversation_tags_TagId",
                schema: "communications",
                table: "conversation_tags",
                column: "TagId");

            migrationBuilder.CreateIndex(
                name: "IX_conversations_ChannelId",
                schema: "communications",
                table: "conversations",
                column: "ChannelId");

            migrationBuilder.CreateIndex(
                name: "IX_conversations_tenant_id_CustomerId",
                schema: "communications",
                table: "conversations",
                columns: new[] { "tenant_id", "CustomerId" });

            migrationBuilder.CreateIndex(
                name: "IX_conversations_tenant_id_Status_LastActivityAt",
                schema: "communications",
                table: "conversations",
                columns: new[] { "tenant_id", "Status", "LastActivityAt" },
                descending: new[] { false, true, true });

            migrationBuilder.CreateIndex(
                name: "IX_conversations_tenant_id_SuggestedCustomerId",
                schema: "communications",
                table: "conversations",
                columns: new[] { "tenant_id", "SuggestedCustomerId" });

            migrationBuilder.CreateIndex(
                name: "IX_idempotency_records_ConversationId",
                schema: "communications",
                table: "idempotency_records",
                column: "ConversationId");

            migrationBuilder.CreateIndex(
                name: "IX_idempotency_records_tenant_id_ConversationId",
                schema: "communications",
                table: "idempotency_records",
                columns: new[] { "tenant_id", "ConversationId" });

            migrationBuilder.CreateIndex(
                name: "IX_idempotency_records_tenant_id_Key",
                schema: "communications",
                table: "idempotency_records",
                columns: new[] { "tenant_id", "Key" },
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_idempotency_records_tenant_id_MessageId",
                schema: "communications",
                table: "idempotency_records",
                columns: new[] { "tenant_id", "MessageId" });

            migrationBuilder.CreateIndex(
                name: "IX_inbound_email_jobs_ChannelId",
                schema: "communications",
                table: "inbound_email_jobs",
                column: "ChannelId");

            migrationBuilder.CreateIndex(
                name: "IX_inbound_email_jobs_InboundReceiptId",
                schema: "communications",
                table: "inbound_email_jobs",
                column: "InboundReceiptId",
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_inbound_email_jobs_tenant_id_InboundReceiptId",
                schema: "communications",
                table: "inbound_email_jobs",
                columns: new[] { "tenant_id", "InboundReceiptId" },
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_inbound_email_jobs_tenant_id_Status_NextAttemptAt",
                schema: "communications",
                table: "inbound_email_jobs",
                columns: new[] { "tenant_id", "Status", "NextAttemptAt" });

            migrationBuilder.CreateIndex(
                name: "IX_inbound_receipts_ChannelId",
                schema: "communications",
                table: "inbound_receipts",
                column: "ChannelId");

            migrationBuilder.CreateIndex(
                name: "IX_inbound_receipts_ConversationMessageId",
                schema: "communications",
                table: "inbound_receipts",
                column: "ConversationMessageId");

            migrationBuilder.CreateIndex(
                name: "IX_inbound_receipts_tenant_id_ChannelId_Provider_ProviderEvent~",
                schema: "communications",
                table: "inbound_receipts",
                columns: new[] { "tenant_id", "ChannelId", "Provider", "ProviderEventId" },
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_inbound_receipts_tenant_id_ChannelId_Provider_RfcMessageId",
                schema: "communications",
                table: "inbound_receipts",
                columns: new[] { "tenant_id", "ChannelId", "Provider", "RfcMessageId" },
                unique: true,
                filter: "\"RfcMessageId\" IS NOT NULL");

            migrationBuilder.CreateIndex(
                name: "IX_message_attachments_MessageId",
                schema: "communications",
                table: "message_attachments",
                column: "MessageId");

            migrationBuilder.CreateIndex(
                name: "IX_message_attachments_tenant_id_MessageId",
                schema: "communications",
                table: "message_attachments",
                columns: new[] { "tenant_id", "MessageId" });

            migrationBuilder.CreateIndex(
                name: "IX_message_attachments_tenant_id_ScanStatus_NextScanAt",
                schema: "communications",
                table: "message_attachments",
                columns: new[] { "tenant_id", "ScanStatus", "NextScanAt" });

            migrationBuilder.CreateIndex(
                name: "IX_message_deliveries_MessageId",
                schema: "communications",
                table: "message_deliveries",
                column: "MessageId");

            migrationBuilder.CreateIndex(
                name: "IX_message_deliveries_RecipientParticipantId",
                schema: "communications",
                table: "message_deliveries",
                column: "RecipientParticipantId");

            migrationBuilder.CreateIndex(
                name: "IX_message_deliveries_tenant_id_MessageId",
                schema: "communications",
                table: "message_deliveries",
                columns: new[] { "tenant_id", "MessageId" });

            migrationBuilder.CreateIndex(
                name: "IX_message_deliveries_tenant_id_RecipientParticipantId",
                schema: "communications",
                table: "message_deliveries",
                columns: new[] { "tenant_id", "RecipientParticipantId" });

            migrationBuilder.CreateIndex(
                name: "IX_message_events_DeliveryId",
                schema: "communications",
                table: "message_events",
                column: "DeliveryId");

            migrationBuilder.CreateIndex(
                name: "IX_message_events_MessageId",
                schema: "communications",
                table: "message_events",
                column: "MessageId");

            migrationBuilder.CreateIndex(
                name: "IX_message_events_tenant_id_MessageId_OccurredAt",
                schema: "communications",
                table: "message_events",
                columns: new[] { "tenant_id", "MessageId", "OccurredAt" });

            migrationBuilder.CreateIndex(
                name: "IX_outbox_jobs_MessageId",
                schema: "communications",
                table: "outbox_jobs",
                column: "MessageId");

            migrationBuilder.CreateIndex(
                name: "IX_outbox_jobs_tenant_id_Status_NextAttemptAt",
                schema: "communications",
                table: "outbox_jobs",
                columns: new[] { "tenant_id", "Status", "NextAttemptAt" });

            migrationBuilder.CreateIndex(
                name: "IX_participants_ChannelId",
                schema: "communications",
                table: "participants",
                column: "ChannelId");

            migrationBuilder.CreateIndex(
                name: "IX_participants_tenant_id_ChannelId_Address",
                schema: "communications",
                table: "participants",
                columns: new[] { "tenant_id", "ChannelId", "Address" },
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_participants_tenant_id_ContactId",
                schema: "communications",
                table: "participants",
                columns: new[] { "tenant_id", "ContactId" });

            migrationBuilder.CreateIndex(
                name: "IX_suppressions_tenant_id_NormalizedEmailAddress",
                schema: "communications",
                table: "suppressions",
                columns: new[] { "tenant_id", "NormalizedEmailAddress" },
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_tags_tenant_id_Name",
                schema: "communications",
                table: "tags",
                columns: new[] { "tenant_id", "Name" },
                unique: true);

            migrationBuilder.EnableTenantRls("communications", "ai_interactions");
            migrationBuilder.EnableTenantRls("communications", "attachment_cleanup_records");
            migrationBuilder.EnableTenantRls("communications", "attachment_uploads");
            migrationBuilder.EnableTenantRls("communications", "channel_credentials");
            migrationBuilder.EnableTenantRls("communications", "channels");
            migrationBuilder.EnableTenantRls("communications", "conversation_customer_candidates");
            migrationBuilder.EnableTenantRls("communications", "conversation_messages");
            migrationBuilder.EnableTenantRls("communications", "conversation_participants");
            migrationBuilder.EnableTenantRls("communications", "conversation_read_states");
            migrationBuilder.EnableTenantRls("communications", "conversation_tags");
            migrationBuilder.EnableTenantRls("communications", "conversations");
            migrationBuilder.EnableTenantRls("communications", "idempotency_records");
            migrationBuilder.EnableTenantRls("communications", "inbound_email_jobs");
            migrationBuilder.EnableTenantRls("communications", "inbound_receipts");
            migrationBuilder.EnableTenantRls("communications", "message_attachments");
            migrationBuilder.EnableTenantRls("communications", "message_deliveries");
            migrationBuilder.EnableTenantRls("communications", "message_events");
            migrationBuilder.EnableTenantRls("communications", "outbox_jobs");
            migrationBuilder.EnableTenantRls("communications", "participants");
            migrationBuilder.EnableTenantRls("communications", "suppressions");
            migrationBuilder.EnableTenantRls("communications", "tags");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "ai_interactions",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "attachment_cleanup_records",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "attachment_uploads",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "channel_credentials",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "conversation_customer_candidates",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "conversation_participants",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "conversation_read_states",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "conversation_tags",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "idempotency_records",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "inbound_email_jobs",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "message_attachments",
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
                name: "tags",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "inbound_receipts",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "message_deliveries",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "conversation_messages",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "conversations",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "participants",
                schema: "communications");

            migrationBuilder.DropTable(
                name: "channels",
                schema: "communications");
        }
    }
}