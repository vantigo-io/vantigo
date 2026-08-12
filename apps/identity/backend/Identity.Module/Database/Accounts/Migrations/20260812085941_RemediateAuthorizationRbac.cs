using System;

using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Identity.Database.Accounts.Migrations
{
    /// <inheritdoc />
    public partial class RemediateAuthorizationRbac : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "role_delegation_boundaries",
                schema: "identity");

            migrationBuilder.AddColumn<string>(
                name: "after_json",
                schema: "identity",
                table: "authorization_audit_events",
                type: "character varying(10000)",
                maxLength: 10000,
                nullable: false,
                defaultValue: "");

            migrationBuilder.AddColumn<string>(
                name: "before_json",
                schema: "identity",
                table: "authorization_audit_events",
                type: "character varying(10000)",
                maxLength: 10000,
                nullable: false,
                defaultValue: "");

            migrationBuilder.AddColumn<string>(
                name: "correlation_id",
                schema: "identity",
                table: "authorization_audit_events",
                type: "character varying(200)",
                maxLength: 200,
                nullable: false,
                defaultValue: "");

            migrationBuilder.AddColumn<bool>(
                name: "mfa_authenticated",
                schema: "identity",
                table: "authorization_audit_events",
                type: "boolean",
                nullable: false,
                defaultValue: false);

            migrationBuilder.CreateTable(
                name: "authorization_delegations",
                schema: "identity",
                columns: table => new
                {
                    id = table.Column<Guid>(type: "uuid", nullable: false),
                    grantee_user_id = table.Column<Guid>(type: "uuid", nullable: false),
                    created_by_user_id = table.Column<Guid>(type: "uuid", nullable: false),
                    expires_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    revoked_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    concurrency_stamp = table.Column<string>(type: "text", nullable: false),
                    can_create_roles = table.Column<bool>(type: "boolean", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_authorization_delegations", x => x.id);
                    table.ForeignKey(
                        name: "fk_authorization_delegations_created_by_user_id",
                        column: x => x.created_by_user_id,
                        principalSchema: "identity",
                        principalTable: "users",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Restrict);
                    table.ForeignKey(
                        name: "fk_authorization_delegations_grantee_user_id",
                        column: x => x.grantee_user_id,
                        principalSchema: "identity",
                        principalTable: "users",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "authorization_delegation_permissions",
                schema: "identity",
                columns: table => new
                {
                    delegation_id = table.Column<Guid>(type: "uuid", nullable: false),
                    permission_key = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_authorization_delegation_permissions", x => new { x.delegation_id, x.permission_key });
                    table.ForeignKey(
                        name: "fk_authorization_delegation_permissions_delegation_id",
                        column: x => x.delegation_id,
                        principalSchema: "identity",
                        principalTable: "authorization_delegations",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "authorization_delegation_roles",
                schema: "identity",
                columns: table => new
                {
                    delegation_id = table.Column<Guid>(type: "uuid", nullable: false),
                    role_id = table.Column<Guid>(type: "uuid", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_authorization_delegation_roles", x => new { x.delegation_id, x.role_id });
                    table.ForeignKey(
                        name: "fk_authorization_delegation_roles_delegation_id",
                        column: x => x.delegation_id,
                        principalSchema: "identity",
                        principalTable: "authorization_delegations",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                    table.ForeignKey(
                        name: "fk_authorization_delegation_roles_role_id",
                        column: x => x.role_id,
                        principalSchema: "identity",
                        principalTable: "roles",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateIndex(
                name: "IX_authorization_delegation_roles_role_id",
                schema: "identity",
                table: "authorization_delegation_roles",
                column: "role_id");

            migrationBuilder.CreateIndex(
                name: "IX_authorization_delegations_created_by_user_id",
                schema: "identity",
                table: "authorization_delegations",
                column: "created_by_user_id");

            migrationBuilder.CreateIndex(
                name: "ix_authorization_delegations_grantee_user_id",
                schema: "identity",
                table: "authorization_delegations",
                column: "grantee_user_id");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "authorization_delegation_permissions",
                schema: "identity");

            migrationBuilder.DropTable(
                name: "authorization_delegation_roles",
                schema: "identity");

            migrationBuilder.DropTable(
                name: "authorization_delegations",
                schema: "identity");

            migrationBuilder.DropColumn(
                name: "after_json",
                schema: "identity",
                table: "authorization_audit_events");

            migrationBuilder.DropColumn(
                name: "before_json",
                schema: "identity",
                table: "authorization_audit_events");

            migrationBuilder.DropColumn(
                name: "correlation_id",
                schema: "identity",
                table: "authorization_audit_events");

            migrationBuilder.DropColumn(
                name: "mfa_authenticated",
                schema: "identity",
                table: "authorization_audit_events");

            migrationBuilder.CreateTable(
                name: "role_delegation_boundaries",
                schema: "identity",
                columns: table => new
                {
                    id = table.Column<Guid>(type: "uuid", nullable: false),
                    concurrency_stamp = table.Column<string>(type: "text", nullable: false),
                    expires_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    permission_key = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false),
                    revoked_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    steward_user_id = table.Column<Guid>(type: "uuid", nullable: false),
                    stewarded_role_id = table.Column<Guid>(type: "uuid", nullable: true)
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
        }
    }
}