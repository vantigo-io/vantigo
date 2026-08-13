using System;

using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Identity.Database.Accounts.Migrations
{
    /// <inheritdoc />
    public partial class FederationConnectionsAndAccessGroups : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.CreateTable(
                name: "access_groups",
                schema: "identity",
                columns: table => new
                {
                    id = table.Column<Guid>(type: "uuid", nullable: false),
                    display_name = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false),
                    source = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: false),
                    external_id = table.Column<string>(type: "character varying(256)", maxLength: 256, nullable: true),
                    is_active = table.Column<bool>(type: "boolean", nullable: false),
                    created_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    updated_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    concurrency_stamp = table.Column<string>(type: "text", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_access_groups", x => x.id);
                });

            migrationBuilder.CreateTable(
                name: "federation_connections",
                schema: "identity",
                columns: table => new
                {
                    id = table.Column<Guid>(type: "uuid", nullable: false),
                    provider_kind = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: false),
                    display_name = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false),
                    authority = table.Column<string>(type: "character varying(2048)", maxLength: 2048, nullable: false),
                    client_id = table.Column<string>(type: "character varying(256)", maxLength: 256, nullable: false),
                    client_secret_reference = table.Column<string>(type: "character varying(2048)", maxLength: 2048, nullable: true),
                    allowed_domains = table.Column<string[]>(type: "text[]", nullable: false),
                    is_enabled = table.Column<bool>(type: "boolean", nullable: false),
                    is_default = table.Column<bool>(type: "boolean", nullable: false),
                    jit_creation_mode = table.Column<string>(type: "character varying(32)", maxLength: 32, nullable: false),
                    configuration_version = table.Column<int>(type: "integer", nullable: false),
                    validation_state = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: false),
                    validation_error_code = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: true),
                    validation_completed_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    validated_configuration_version = table.Column<int>(type: "integer", nullable: true),
                    validated_issuer = table.Column<string>(type: "character varying(2048)", maxLength: 2048, nullable: true),
                    validated_discovery_endpoint = table.Column<string>(type: "character varying(2048)", maxLength: 2048, nullable: true),
                    validated_authorization_endpoint = table.Column<string>(type: "character varying(2048)", maxLength: 2048, nullable: true),
                    validated_token_endpoint = table.Column<string>(type: "character varying(2048)", maxLength: 2048, nullable: true),
                    validated_jwks_uri = table.Column<string>(type: "character varying(2048)", maxLength: 2048, nullable: true),
                    concurrency_stamp = table.Column<string>(type: "text", nullable: false),
                    created_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    updated_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_federation_connections", x => x.id);
                });

            migrationBuilder.CreateTable(
                name: "access_group_memberships",
                schema: "identity",
                columns: table => new
                {
                    group_id = table.Column<Guid>(type: "uuid", nullable: false),
                    user_id = table.Column<Guid>(type: "uuid", nullable: false),
                    source = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: false),
                    is_upstream_present = table.Column<bool>(type: "boolean", nullable: false),
                    membership_override = table.Column<string>(type: "character varying(32)", maxLength: 32, nullable: true),
                    updated_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_access_group_memberships", x => new { x.group_id, x.user_id });
                    table.ForeignKey(
                        name: "fk_access_group_memberships_access_groups_group_id",
                        column: x => x.group_id,
                        principalSchema: "identity",
                        principalTable: "access_groups",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                    table.ForeignKey(
                        name: "fk_access_group_memberships_users_user_id",
                        column: x => x.user_id,
                        principalSchema: "identity",
                        principalTable: "users",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "access_group_role_mappings",
                schema: "identity",
                columns: table => new
                {
                    group_id = table.Column<Guid>(type: "uuid", nullable: false),
                    role_id = table.Column<Guid>(type: "uuid", nullable: false),
                    source = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: false),
                    created_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_access_group_role_mappings", x => new { x.group_id, x.role_id });
                    table.ForeignKey(
                        name: "fk_access_group_role_mappings_access_groups_group_id",
                        column: x => x.group_id,
                        principalSchema: "identity",
                        principalTable: "access_groups",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                    table.ForeignKey(
                        name: "fk_access_group_role_mappings_roles_role_id",
                        column: x => x.role_id,
                        principalSchema: "identity",
                        principalTable: "roles",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateIndex(
                name: "ix_access_group_memberships_user_id",
                schema: "identity",
                table: "access_group_memberships",
                column: "user_id");

            migrationBuilder.CreateIndex(
                name: "ix_access_group_role_mappings_role_id",
                schema: "identity",
                table: "access_group_role_mappings",
                column: "role_id");

            migrationBuilder.CreateIndex(
                name: "ux_access_groups_display_name",
                schema: "identity",
                table: "access_groups",
                column: "display_name",
                unique: true);

            migrationBuilder.CreateIndex(
                name: "ux_access_groups_external_id",
                schema: "identity",
                table: "access_groups",
                column: "external_id",
                unique: true,
                filter: "external_id IS NOT NULL");

            migrationBuilder.CreateIndex(
                name: "ux_federation_connections_default",
                schema: "identity",
                table: "federation_connections",
                column: "is_default",
                unique: true,
                filter: "is_default = TRUE");

            migrationBuilder.CreateIndex(
                name: "ux_federation_connections_display_name",
                schema: "identity",
                table: "federation_connections",
                column: "display_name",
                unique: true);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "access_group_memberships",
                schema: "identity");

            migrationBuilder.DropTable(
                name: "access_group_role_mappings",
                schema: "identity");

            migrationBuilder.DropTable(
                name: "federation_connections",
                schema: "identity");

            migrationBuilder.DropTable(
                name: "access_groups",
                schema: "identity");
        }
    }
}