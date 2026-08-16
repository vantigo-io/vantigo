using System;

using Microsoft.EntityFrameworkCore.Migrations;

using Npgsql.EntityFrameworkCore.PostgreSQL.Metadata;

using Vantigo.Tenancy.EntityFramework;

#nullable disable

namespace Vantigo.Customers.Database.Customers.Migrations
{
    /// <inheritdoc />
    public partial class Initial : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.EnsureSchema(
                name: "customers");

            migrationBuilder.CreateTable(
                name: "contacts",
                schema: "customers",
                columns: table => new
                {
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    id = table.Column<int>(type: "integer", nullable: false, comment: "The unique identifier of the contact")
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    first_name = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: false, comment: "The given name of the contact"),
                    last_name = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: false, comment: "The family name of the contact"),
                    middle_name = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: true, comment: "The middle name of the contact, when known"),
                    prefix = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: true, comment: "An honorific prefix such as 'Dr.', when known"),
                    suffix = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: true, comment: "An honorific suffix such as 'Jr.' or 'PhD', when known"),
                    phone = table.Column<string>(type: "character varying(30)", unicode: false, maxLength: 30, nullable: true, comment: "The phone number the contact can be reached on, when known"),
                    email = table.Column<string>(type: "character varying(255)", unicode: false, maxLength: 255, nullable: true, comment: "The email address the contact can be reached on, when known")
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_contacts", x => new { x.tenant_id, x.id });
                });

            migrationBuilder.CreateTable(
                name: "customers",
                schema: "customers",
                columns: table => new
                {
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    id = table.Column<int>(type: "integer", nullable: false, comment: "The unique identifier of the customer")
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    customer_number = table.Column<long>(type: "bigint", nullable: false),
                    name = table.Column<string>(type: "character varying(255)", maxLength: 255, nullable: false, comment: "The friendly name of the customer"),
                    legal_country = table.Column<string>(type: "character varying(2)", unicode: false, maxLength: 2, nullable: true, comment: "The legal country of the identity that is associated with the customer"),
                    legal_id = table.Column<string>(type: "character varying(50)", unicode: false, maxLength: 50, nullable: true, comment: "The legal id of the identity that is associated with the customer"),
                    legal_name = table.Column<string>(type: "character varying(255)", maxLength: 255, nullable: true, comment: "The legal name of the identity that is associated with the customer"),
                    legal_source = table.Column<string>(type: "character varying(50)", unicode: false, maxLength: 50, nullable: true, comment: "Where the legal identity data of the customer was retrieved from"),
                    legal_type = table.Column<string>(type: "character varying(50)", unicode: false, maxLength: 50, nullable: true, comment: "The legal type of the identity that is associated with the customer")
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_customers", x => new { x.tenant_id, x.id });
                });

            migrationBuilder.CreateTable(
                name: "customers_contacts",
                schema: "customers",
                columns: table => new
                {
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    customer_id = table.Column<int>(type: "integer", nullable: false, comment: "The id of the customer the contact is associated with"),
                    contact_id = table.Column<int>(type: "integer", nullable: false, comment: "The id of the associated contact"),
                    role = table.Column<string>(type: "character varying(255)", maxLength: 255, nullable: false, comment: "The role the contact holds for the customer, such as 'CEO' or 'Custodian'"),
                    phone = table.Column<string>(type: "character varying(30)", unicode: false, maxLength: 30, nullable: true, comment: "A connection-specific phone number, when it differs from the contact's own"),
                    email = table.Column<string>(type: "character varying(255)", unicode: false, maxLength: 255, nullable: true, comment: "A connection-specific email address, when it differs from the contact's own")
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_customers_contacts", x => new { x.tenant_id, x.customer_id, x.contact_id });
                    table.ForeignKey(
                        name: "FK_customers_contacts_contacts_tenant_id_contact_id",
                        columns: x => new { x.tenant_id, x.contact_id },
                        principalSchema: "customers",
                        principalTable: "contacts",
                        principalColumns: new[] { "tenant_id", "id" },
                        onDelete: ReferentialAction.Cascade);
                    table.ForeignKey(
                        name: "FK_customers_contacts_customers_tenant_id_customer_id",
                        columns: x => new { x.tenant_id, x.customer_id },
                        principalSchema: "customers",
                        principalTable: "customers",
                        principalColumns: new[] { "tenant_id", "id" },
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "customers_timeline_entries",
                schema: "customers",
                columns: table => new
                {
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
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
                    table.PrimaryKey("PK_customers_timeline_entries", x => new { x.tenant_id, x.id });
                    table.ForeignKey(
                        name: "FK_customers_timeline_entries_customers_tenant_id_customer_id",
                        columns: x => new { x.tenant_id, x.customer_id },
                        principalSchema: "customers",
                        principalTable: "customers",
                        principalColumns: new[] { "tenant_id", "id" },
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "customers_timeline_entries_revisions",
                schema: "customers",
                columns: table => new
                {
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
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
                    table.PrimaryKey("PK_customers_timeline_entries_revisions", x => new { x.tenant_id, x.id });
                    table.ForeignKey(
                        name: "FK_customers_timeline_entries_revisions_customers_timeline_ent~",
                        columns: x => new { x.tenant_id, x.customer_timeline_entry_id },
                        principalSchema: "customers",
                        principalTable: "customers_timeline_entries",
                        principalColumns: new[] { "tenant_id", "id" },
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateIndex(
                name: "ux_customers_tenant_customer_number",
                schema: "customers",
                table: "customers",
                columns: new[] { "tenant_id", "customer_number" },
                unique: true);

            migrationBuilder.CreateIndex(
                name: "ix_customers_contacts_tenant_contact_id",
                schema: "customers",
                table: "customers_contacts",
                columns: new[] { "tenant_id", "contact_id" });

            migrationBuilder.CreateIndex(
                name: "ix_customers_timeline_entries_tenant_customer_id",
                schema: "customers",
                table: "customers_timeline_entries",
                columns: new[] { "tenant_id", "customer_id" });

            migrationBuilder.CreateIndex(
                name: "ux_customers_timeline_entries_revisions_entry_revision",
                schema: "customers",
                table: "customers_timeline_entries_revisions",
                columns: new[] { "tenant_id", "customer_timeline_entry_id", "revision_number" },
                unique: true);

            migrationBuilder.EnableTenantRls("customers", "contacts");
            migrationBuilder.EnableTenantRls("customers", "customers");
            migrationBuilder.EnableTenantRls("customers", "customers_contacts");
            migrationBuilder.EnableTenantRls("customers", "customers_timeline_entries");
            migrationBuilder.EnableTenantRls("customers", "customers_timeline_entries_revisions");
            migrationBuilder.AddTenantCountersTable("customers");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.Sql("DROP TABLE IF EXISTS \"customers\".\"tenant_counters\";");

            migrationBuilder.DropTable(
                name: "customers_contacts",
                schema: "customers");

            migrationBuilder.DropTable(
                name: "customers_timeline_entries_revisions",
                schema: "customers");

            migrationBuilder.DropTable(
                name: "contacts",
                schema: "customers");

            migrationBuilder.DropTable(
                name: "customers_timeline_entries",
                schema: "customers");

            migrationBuilder.DropTable(
                name: "customers",
                schema: "customers");
        }
    }
}