using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Identity.Database.Accounts.Migrations
{
    /// <inheritdoc />
    public partial class ScimProvisioningFoundationHardened : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropIndex("ux_access_groups_external_id", "access_groups", "identity");
            migrationBuilder.AddColumn<Guid>(name: "scim_connection_id", schema: "identity", table: "access_groups", type: "uuid", nullable: true);
            migrationBuilder.CreateTable(name: "scim_connections", schema: "identity", columns: table => new
            {
                id = table.Column<Guid>("uuid", nullable: false),
                federation_connection_id = table.Column<Guid>("uuid", nullable: false),
                mode = table.Column<string>("character varying(32)", maxLength: 32, nullable: false),
                is_enabled = table.Column<bool>("boolean", nullable: false),
                token_version = table.Column<int>("integer", nullable: false),
                created_at = table.Column<DateTimeOffset>("timestamp with time zone", nullable: false),
                updated_at = table.Column<DateTimeOffset>("timestamp with time zone", nullable: false),
                last_rotated_at = table.Column<DateTimeOffset>("timestamp with time zone", nullable: true),
                last_revoked_at = table.Column<DateTimeOffset>("timestamp with time zone", nullable: true),
                concurrency_stamp = table.Column<string>("text", nullable: false)
            }, constraints: table =>
            {
                table.PrimaryKey("pk_scim_connections", x => x.id);
                table.ForeignKey(name: "fk_scim_connections_federation_connections_id", columns: x => x.federation_connection_id, principalSchema: "identity", principalTable: "federation_connections", principalColumns: new[] { "id" }, onDelete: ReferentialAction.Cascade);
            });
            migrationBuilder.CreateTable(name: "scim_bearer_tokens", schema: "identity", columns: table => new
            {
                id = table.Column<Guid>("uuid", nullable: false),
                scim_connection_id = table.Column<Guid>("uuid", nullable: false),
                version = table.Column<int>("integer", nullable: false),
                token_hash = table.Column<string>("character varying(64)", maxLength: 64, nullable: false),
                created_at = table.Column<DateTimeOffset>("timestamp with time zone", nullable: false),
                expires_at = table.Column<DateTimeOffset>("timestamp with time zone", nullable: true),
                revoked_at = table.Column<DateTimeOffset>("timestamp with time zone", nullable: true),
                is_current = table.Column<bool>("boolean", nullable: false)
            }, constraints: table =>
            {
                table.PrimaryKey("pk_scim_bearer_tokens", x => x.id);
                table.ForeignKey(name: "fk_scim_bearer_tokens_connections_id", columns: x => x.scim_connection_id, principalSchema: "identity", principalTable: "scim_connections", principalColumns: new[] { "id" }, onDelete: ReferentialAction.Cascade);
            });
            migrationBuilder.CreateTable(name: "scim_user_mappings", schema: "identity", columns: table => new
            {
                id = table.Column<Guid>("uuid", nullable: false),
                scim_connection_id = table.Column<Guid>("uuid", nullable: false),
                user_id = table.Column<Guid>("uuid", nullable: false),
                resource_id = table.Column<string>("character varying(256)", maxLength: 256, nullable: false),
                external_id = table.Column<string>("character varying(512)", maxLength: 512, nullable: false),
                user_name = table.Column<string>("character varying(512)", maxLength: 512, nullable: false),
                upstream_active = table.Column<bool>("boolean", nullable: false),
                lifecycle_override = table.Column<string>("character varying(32)", maxLength: 32, nullable: true),
                lifecycle_override_reason = table.Column<string>("character varying(2000)", maxLength: 2000, nullable: true),
                source_profile_json = table.Column<string>("character varying(20000)", maxLength: 20000, nullable: true),
                last_synchronized_at = table.Column<DateTimeOffset>("timestamp with time zone", nullable: false),
                version = table.Column<int>("integer", nullable: false),
                etag = table.Column<string>("character varying(128)", maxLength: 128, nullable: false),
                created_at = table.Column<DateTimeOffset>("timestamp with time zone", nullable: false),
                updated_at = table.Column<DateTimeOffset>("timestamp with time zone", nullable: false)
            }, constraints: table =>
            {
                table.PrimaryKey("pk_scim_user_mappings", x => x.id);
                table.ForeignKey(name: "fk_scim_user_mappings_connections_id", columns: x => x.scim_connection_id, principalSchema: "identity", principalTable: "scim_connections", principalColumns: new[] { "id" }, onDelete: ReferentialAction.Cascade);
                table.ForeignKey(name: "fk_scim_user_mappings_users_id", columns: x => x.user_id, principalSchema: "identity", principalTable: "users", principalColumns: new[] { "id" }, onDelete: ReferentialAction.Cascade);
            });
            migrationBuilder.CreateIndex("ux_access_groups_scim_external_id", "access_groups", new[] { "scim_connection_id", "external_id" }, "identity", unique: true, filter: "external_id IS NOT NULL");
            migrationBuilder.CreateIndex("ux_scim_bearer_tokens_connection_version", "scim_bearer_tokens", new[] { "scim_connection_id", "version" }, "identity", unique: true);
            migrationBuilder.CreateIndex("ux_scim_bearer_tokens_hash", "scim_bearer_tokens", "token_hash", "identity", unique: true);
            migrationBuilder.CreateIndex("ux_scim_connections_federation_connection_id", "scim_connections", "federation_connection_id", "identity", unique: true);
            migrationBuilder.CreateIndex("ix_scim_user_mappings_user_id", "scim_user_mappings", "user_id", "identity");
            migrationBuilder.CreateIndex("ux_scim_user_mappings_external_id", "scim_user_mappings", new[] { "scim_connection_id", "external_id" }, "identity", unique: true);
            migrationBuilder.CreateIndex("ux_scim_user_mappings_resource", "scim_user_mappings", new[] { "scim_connection_id", "resource_id" }, "identity", unique: true);
            migrationBuilder.CreateIndex("ux_scim_user_mappings_user", "scim_user_mappings", new[] { "scim_connection_id", "user_id" }, "identity", unique: true);
            migrationBuilder.AddForeignKey(name: "fk_access_groups_scim_connections_id", schema: "identity", table: "access_groups", column: "scim_connection_id", principalSchema: "identity", principalTable: "scim_connections", principalColumn: "id", onDelete: ReferentialAction.Cascade);
            migrationBuilder.DropForeignKey(
                name: "fk_federated_identities_connections_connection_id",
                schema: "identity",
                table: "federated_identities");

            migrationBuilder.DropForeignKey(
                name: "fk_federated_identities_users_user_id",
                schema: "identity",
                table: "federated_identities");

            migrationBuilder.AddForeignKey(
                name: "fk_federated_identities_connections_connection_id",
                schema: "identity",
                table: "federated_identities",
                column: "connection_id",
                principalSchema: "identity",
                principalTable: "federation_connections",
                principalColumn: "id",
                onDelete: ReferentialAction.Restrict);

            migrationBuilder.AddForeignKey(
                name: "fk_federated_identities_users_user_id",
                schema: "identity",
                table: "federated_identities",
                column: "user_id",
                principalSchema: "identity",
                principalTable: "users",
                principalColumn: "id",
                onDelete: ReferentialAction.Restrict);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropForeignKey(name: "fk_access_groups_scim_connections_id", schema: "identity", table: "access_groups");
            migrationBuilder.DropTable("scim_bearer_tokens", "identity");
            migrationBuilder.DropTable("scim_user_mappings", "identity");
            migrationBuilder.DropTable("scim_connections", "identity");
            migrationBuilder.DropIndex("ux_access_groups_scim_external_id", "access_groups", "identity");
            migrationBuilder.DropColumn("scim_connection_id", "access_groups", "identity");
            migrationBuilder.CreateIndex("ux_access_groups_external_id", "access_groups", "external_id", "identity", unique: true, filter: "external_id IS NOT NULL");
            migrationBuilder.DropForeignKey(
                name: "fk_federated_identities_connections_connection_id",
                schema: "identity",
                table: "federated_identities");

            migrationBuilder.DropForeignKey(
                name: "fk_federated_identities_users_user_id",
                schema: "identity",
                table: "federated_identities");

            migrationBuilder.AddForeignKey(
                name: "fk_federated_identities_connections_connection_id",
                schema: "identity",
                table: "federated_identities",
                column: "connection_id",
                principalSchema: "identity",
                principalTable: "federation_connections",
                principalColumn: "id",
                onDelete: ReferentialAction.Cascade);

            migrationBuilder.AddForeignKey(
                name: "fk_federated_identities_users_user_id",
                schema: "identity",
                table: "federated_identities",
                column: "user_id",
                principalSchema: "identity",
                principalTable: "users",
                principalColumn: "id",
                onDelete: ReferentialAction.Cascade);
        }
    }
}