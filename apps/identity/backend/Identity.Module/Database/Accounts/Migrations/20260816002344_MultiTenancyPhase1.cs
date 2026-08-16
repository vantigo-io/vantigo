using System;

using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Identity.Database.Accounts.Migrations
{
    /// <inheritdoc />
    public partial class MultiTenancyPhase1 : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AddColumn<Guid>(
                name: "active_tenant_id",
                schema: "identity",
                table: "users",
                type: "uuid",
                nullable: true);

            migrationBuilder.AddColumn<Guid>(
                name: "tenant_id",
                schema: "identity",
                table: "user_roles",
                type: "uuid",
                nullable: true);

            migrationBuilder.AddColumn<Guid>(
                name: "tenant_id",
                schema: "identity",
                table: "invitations",
                type: "uuid",
                nullable: true);

            migrationBuilder.AddColumn<Guid>(
                name: "tenant_id",
                schema: "identity",
                table: "access_group_role_mappings",
                type: "uuid",
                nullable: true);

            migrationBuilder.AddColumn<Guid>(
                name: "tenant_id",
                schema: "identity",
                table: "access_group_memberships",
                type: "uuid",
                nullable: true);

            migrationBuilder.CreateTable(
                name: "tenants",
                schema: "identity",
                columns: table => new
                {
                    id = table.Column<Guid>(type: "uuid", nullable: false),
                    name = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false),
                    slug = table.Column<string>(type: "character varying(63)", maxLength: 63, nullable: false),
                    status = table.Column<string>(type: "character varying(16)", maxLength: 16, nullable: false),
                    enabled_modules = table.Column<string[]>(type: "text[]", nullable: false),
                    created_at_utc = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_tenants", x => x.id);
                });

            migrationBuilder.CreateTable(
                name: "tenant_memberships",
                schema: "identity",
                columns: table => new
                {
                    user_id = table.Column<Guid>(type: "uuid", nullable: false),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    created_at_utc = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_tenant_memberships", x => new { x.user_id, x.tenant_id });
                    table.ForeignKey(
                        name: "fk_tenant_memberships_tenants_tenant_id",
                        column: x => x.tenant_id,
                        principalSchema: "identity",
                        principalTable: "tenants",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                    table.ForeignKey(
                        name: "fk_tenant_memberships_users_user_id",
                        column: x => x.user_id,
                        principalSchema: "identity",
                        principalTable: "users",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "tenant_sso_configurations",
                schema: "identity",
                columns: table => new
                {
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    entra_tenant_id = table.Column<Guid>(type: "uuid", nullable: false),
                    allowed_email_domain = table.Column<string>(type: "character varying(255)", maxLength: 255, nullable: true),
                    jit_provisioning_enabled = table.Column<bool>(type: "boolean", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_tenant_sso_configurations", x => x.tenant_id);
                    table.ForeignKey(
                        name: "fk_tenant_sso_configurations_tenants_tenant_id",
                        column: x => x.tenant_id,
                        principalSchema: "identity",
                        principalTable: "tenants",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateIndex(
                name: "ux_user_roles_user_role_tenant",
                schema: "identity",
                table: "user_roles",
                columns: new[] { "user_id", "role_id", "tenant_id" },
                unique: true);

            migrationBuilder.CreateIndex(
                name: "ux_access_group_role_mappings_group_role_tenant",
                schema: "identity",
                table: "access_group_role_mappings",
                columns: new[] { "group_id", "role_id", "tenant_id" },
                unique: true);

            migrationBuilder.CreateIndex(
                name: "ux_access_group_memberships_group_user_tenant",
                schema: "identity",
                table: "access_group_memberships",
                columns: new[] { "group_id", "user_id", "tenant_id" },
                unique: true);

            migrationBuilder.CreateIndex(
                name: "ix_tenant_memberships_tenant_id",
                schema: "identity",
                table: "tenant_memberships",
                column: "tenant_id");

            migrationBuilder.CreateIndex(
                name: "ux_tenants_slug",
                schema: "identity",
                table: "tenants",
                column: "slug",
                unique: true);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "tenant_memberships",
                schema: "identity");

            migrationBuilder.DropTable(
                name: "tenant_sso_configurations",
                schema: "identity");

            migrationBuilder.DropTable(
                name: "tenants",
                schema: "identity");

            migrationBuilder.DropIndex(
                name: "ux_user_roles_user_role_tenant",
                schema: "identity",
                table: "user_roles");

            migrationBuilder.DropIndex(
                name: "ux_access_group_role_mappings_group_role_tenant",
                schema: "identity",
                table: "access_group_role_mappings");

            migrationBuilder.DropIndex(
                name: "ux_access_group_memberships_group_user_tenant",
                schema: "identity",
                table: "access_group_memberships");

            migrationBuilder.DropColumn(
                name: "active_tenant_id",
                schema: "identity",
                table: "users");

            migrationBuilder.DropColumn(
                name: "tenant_id",
                schema: "identity",
                table: "user_roles");

            migrationBuilder.DropColumn(
                name: "tenant_id",
                schema: "identity",
                table: "invitations");

            migrationBuilder.DropColumn(
                name: "tenant_id",
                schema: "identity",
                table: "access_group_role_mappings");

            migrationBuilder.DropColumn(
                name: "tenant_id",
                schema: "identity",
                table: "access_group_memberships");
        }
    }
}