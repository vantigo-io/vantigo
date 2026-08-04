using System;
using Microsoft.EntityFrameworkCore.Migrations;
using Npgsql.EntityFrameworkCore.PostgreSQL.Metadata;

#nullable disable

namespace Vantigo.Customers.Api.Database.Customers.Migrations
{
    /// <inheritdoc />
    public partial class AddCustomerTimeline : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.CreateTable(
                name: "customers_timeline_entries",
                columns: table => new
                {
                    id = table.Column<int>(type: "integer", nullable: false)
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    customer_id = table.Column<int>(type: "integer", nullable: false),
                    provenance = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: false),
                    producer = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: false),
                    event_type = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: false),
                    occurred_on = table.Column<DateOnly>(type: "date", nullable: false),
                    occurred_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    summary = table.Column<string>(type: "character varying(500)", maxLength: 500, nullable: true),
                    note = table.Column<string>(type: "character varying(10000)", maxLength: 10000, nullable: true),
                    source_url = table.Column<string>(type: "character varying(2048)", maxLength: 2048, nullable: true),
                    payload_json = table.Column<string>(type: "jsonb", nullable: true),
                    payload_version = table.Column<int>(type: "integer", nullable: false),
                    current_revision = table.Column<int>(type: "integer", nullable: false),
                    state = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: false),
                    actor_kind = table.Column<string>(type: "character varying(30)", maxLength: 30, nullable: false),
                    actor_display = table.Column<string>(type: "character varying(255)", maxLength: 255, nullable: true),
                    created_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    updated_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    deleted_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_customers_timeline_entries", x => x.id);
                    table.ForeignKey(
                        name: "FK_customers_timeline_entries_customers_customer_id",
                        column: x => x.customer_id,
                        principalTable: "customers",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "customers_timeline_entries_revisions",
                columns: table => new
                {
                    id = table.Column<int>(type: "integer", nullable: false)
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    customer_timeline_entry_id = table.Column<int>(type: "integer", nullable: false),
                    revision_number = table.Column<int>(type: "integer", nullable: false),
                    provenance = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: false),
                    producer = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: false),
                    event_type = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: false),
                    occurred_on = table.Column<DateOnly>(type: "date", nullable: false),
                    occurred_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    summary = table.Column<string>(type: "character varying(500)", maxLength: 500, nullable: true),
                    note = table.Column<string>(type: "character varying(10000)", maxLength: 10000, nullable: true),
                    source_url = table.Column<string>(type: "character varying(2048)", maxLength: 2048, nullable: true),
                    payload_json = table.Column<string>(type: "jsonb", nullable: true),
                    payload_version = table.Column<int>(type: "integer", nullable: false),
                    state = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: false),
                    actor_kind = table.Column<string>(type: "character varying(30)", maxLength: 30, nullable: false),
                    actor_display = table.Column<string>(type: "character varying(255)", maxLength: 255, nullable: true),
                    created_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    deleted_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_customers_timeline_entries_revisions", x => x.id);
                    table.ForeignKey(
                        name: "FK_customers_timeline_entries_revisions_customers_timeline_ent~",
                        column: x => x.customer_timeline_entry_id,
                        principalTable: "customers_timeline_entries",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateIndex(
                name: "IX_customers_timeline_entries_customer_id",
                table: "customers_timeline_entries",
                column: "customer_id");

            migrationBuilder.CreateIndex(
                name: "ux_customers_timeline_entries_revisions_entry_revision",
                table: "customers_timeline_entries_revisions",
                columns: new[] { "customer_timeline_entry_id", "revision_number" },
                unique: true);

            // Keep the feed's NULLS LAST ordering aligned with the keyset query.
            migrationBuilder.Sql(
                "CREATE INDEX ix_customers_timeline_entries_feed ON customers_timeline_entries " +
                "(customer_id, state, occurred_on DESC, (occurred_at IS NOT NULL) DESC, occurred_at DESC, id DESC);");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "customers_timeline_entries_revisions");

            migrationBuilder.Sql("DROP INDEX IF EXISTS ix_customers_timeline_entries_feed;");
            migrationBuilder.DropTable(
                name: "customers_timeline_entries");
        }
    }
}
