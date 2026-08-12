using System;

using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Identity.Database.Accounts.Migrations
{
    /// <inheritdoc />
    public partial class AccountSettingsAndPasskeys : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AddColumn<string>(
                name: "avatar_content_type",
                schema: "identity",
                table: "users",
                type: "character varying(64)",
                maxLength: 64,
                nullable: true);

            migrationBuilder.AddColumn<byte[]>(
                name: "avatar_data",
                schema: "identity",
                table: "users",
                type: "bytea",
                nullable: true);

            migrationBuilder.AddColumn<string>(
                name: "preferred_language",
                schema: "identity",
                table: "users",
                type: "character varying(10)",
                maxLength: 10,
                nullable: true);

            migrationBuilder.CreateTable(
                name: "passkey_ceremonies",
                schema: "identity",
                columns: table => new
                {
                    id = table.Column<Guid>(type: "uuid", nullable: false),
                    user_id = table.Column<Guid>(type: "uuid", nullable: false),
                    kind = table.Column<string>(type: "character varying(32)", maxLength: 32, nullable: false),
                    state = table.Column<string>(type: "character varying(20000)", maxLength: 20000, nullable: false),
                    credential_name = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: true),
                    expires_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    consumed = table.Column<bool>(type: "boolean", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_passkey_ceremonies", x => x.id);
                    table.ForeignKey(
                        name: "fk_passkey_ceremonies_users_user_id",
                        column: x => x.user_id,
                        principalSchema: "identity",
                        principalTable: "users",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "user_passkeys",
                schema: "identity",
                columns: table => new
                {
                    user_id = table.Column<Guid>(type: "uuid", nullable: false),
                    credential_id = table.Column<byte[]>(type: "bytea", nullable: false),
                    data = table.Column<string>(type: "text", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_user_passkeys", x => new { x.user_id, x.credential_id });
                    table.ForeignKey(
                        name: "fk_user_passkeys_users_user_id",
                        column: x => x.user_id,
                        principalSchema: "identity",
                        principalTable: "users",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateIndex(
                name: "ix_passkey_ceremonies_expiration",
                schema: "identity",
                table: "passkey_ceremonies",
                columns: new[] { "expires_at", "consumed" });

            migrationBuilder.CreateIndex(
                name: "IX_passkey_ceremonies_user_id",
                schema: "identity",
                table: "passkey_ceremonies",
                column: "user_id");

            migrationBuilder.CreateIndex(
                name: "ux_user_passkeys_credential_id",
                schema: "identity",
                table: "user_passkeys",
                column: "credential_id",
                unique: true);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "passkey_ceremonies",
                schema: "identity");

            migrationBuilder.DropTable(
                name: "user_passkeys",
                schema: "identity");

            migrationBuilder.DropColumn(
                name: "avatar_content_type",
                schema: "identity",
                table: "users");

            migrationBuilder.DropColumn(
                name: "avatar_data",
                schema: "identity",
                table: "users");

            migrationBuilder.DropColumn(
                name: "preferred_language",
                schema: "identity",
                table: "users");
        }
    }
}