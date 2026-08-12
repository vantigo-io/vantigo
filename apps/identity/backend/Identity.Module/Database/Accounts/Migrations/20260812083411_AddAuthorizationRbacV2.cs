using System;

using Microsoft.EntityFrameworkCore.Migrations;

using Npgsql.EntityFrameworkCore.PostgreSQL.Metadata;

#nullable disable

namespace Vantigo.Identity.Database.Accounts.Migrations
{
    /// <inheritdoc />
    public partial class AddAuthorizationRbacV2 : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.CreateTable(
                name: "authorization_audit_events",
                schema: "identity",
                columns: table => new
                {
                    id = table.Column<long>(type: "bigint", nullable: false)
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    actor_user_id = table.Column<Guid>(type: "uuid", nullable: true),
                    target_user_id = table.Column<Guid>(type: "uuid", nullable: true),
                    target_role_id = table.Column<Guid>(type: "uuid", nullable: true),
                    action = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: false),
                    details = table.Column<string>(type: "character varying(4000)", maxLength: 4000, nullable: false),
                    occurred_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_authorization_audit_events", x => x.id);
                });

            migrationBuilder.CreateTable(
                name: "role_delegation_boundaries",
                schema: "identity",
                columns: table => new
                {
                    id = table.Column<Guid>(type: "uuid", nullable: false),
                    steward_user_id = table.Column<Guid>(type: "uuid", nullable: false),
                    stewarded_role_id = table.Column<Guid>(type: "uuid", nullable: true),
                    permission_key = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false),
                    expires_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    revoked_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    concurrency_stamp = table.Column<string>(type: "text", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_role_delegation_boundaries", x => x.id);
                    table.ForeignKey(
                        name: "fk_role_delegation_boundaries_steward_user_id",
                        column: x => x.steward_user_id,
                        principalSchema: "identity",
                        principalTable: "users",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                    table.ForeignKey(
                        name: "fk_role_delegation_boundaries_stewarded_role_id",
                        column: x => x.stewarded_role_id,
                        principalSchema: "identity",
                        principalTable: "roles",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "role_metadata",
                schema: "identity",
                columns: table => new
                {
                    role_id = table.Column<Guid>(type: "uuid", nullable: false),
                    display_name = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false),
                    description = table.Column<string>(type: "character varying(2000)", maxLength: 2000, nullable: false),
                    is_system = table.Column<bool>(type: "boolean", nullable: false),
                    is_built_in = table.Column<bool>(type: "boolean", nullable: false),
                    steward_user_id = table.Column<Guid>(type: "uuid", nullable: true),
                    concurrency_stamp = table.Column<string>(type: "text", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_role_metadata", x => x.role_id);
                    table.ForeignKey(
                        name: "fk_role_metadata_roles_role_id",
                        column: x => x.role_id,
                        principalSchema: "identity",
                        principalTable: "roles",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "role_permissions",
                schema: "identity",
                columns: table => new
                {
                    role_id = table.Column<Guid>(type: "uuid", nullable: false),
                    permission_key = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_role_permissions", x => new { x.role_id, x.permission_key });
                    table.ForeignKey(
                        name: "fk_role_permissions_roles_role_id",
                        column: x => x.role_id,
                        principalSchema: "identity",
                        principalTable: "roles",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateIndex(
                name: "ix_authorization_audit_events_occurred_at",
                schema: "identity",
                table: "authorization_audit_events",
                column: "occurred_at");

            migrationBuilder.CreateIndex(
                name: "ix_role_delegation_boundaries_steward_permission",
                schema: "identity",
                table: "role_delegation_boundaries",
                columns: new[] { "steward_user_id", "permission_key" });

            migrationBuilder.CreateIndex(
                name: "IX_role_delegation_boundaries_stewarded_role_id",
                schema: "identity",
                table: "role_delegation_boundaries",
                column: "stewarded_role_id");

            migrationBuilder.CreateIndex(
                name: "ix_role_metadata_steward_user_id",
                schema: "identity",
                table: "role_metadata",
                column: "steward_user_id");

            migrationBuilder.CreateIndex(
                name: "ix_role_permissions_permission_key",
                schema: "identity",
                table: "role_permissions",
                column: "permission_key");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "authorization_audit_events",
                schema: "identity");

            migrationBuilder.DropTable(
                name: "role_delegation_boundaries",
                schema: "identity");

            migrationBuilder.DropTable(
                name: "role_metadata",
                schema: "identity");

            migrationBuilder.DropTable(
                name: "role_permissions",
                schema: "identity");
        }
    }
}