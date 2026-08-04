using System;

using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Customers.Api.Database.Accounts.Migrations;

public partial class AddInvitations : Migration
{
    protected override void Up(MigrationBuilder migrationBuilder)
    {
        migrationBuilder.CreateTable(
            name: "invitations",
            schema: "accounts",
            columns: table => new
            {
                id = table.Column<Guid>(type: "uuid", nullable: false),
                email = table.Column<string>(type: "character varying(256)", maxLength: 256, nullable: false),
                normalized_email = table.Column<string>(type: "character varying(256)", maxLength: 256, nullable: false),
                role = table.Column<string>(type: "character varying(32)", maxLength: 32, nullable: false),
                display_name = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: true),
                token_hash = table.Column<string>(type: "character varying(64)", maxLength: 64, nullable: false),
                created_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                expires_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                revoked_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                accepted_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                invited_by_user_id = table.Column<Guid>(type: "uuid", nullable: false)
            },
            constraints: table => table.PrimaryKey("pk_invitations", x => x.id));

        migrationBuilder.CreateIndex("ux_invitations_token_hash", "invitations", "token_hash", "accounts", unique: true);
        migrationBuilder.CreateIndex(
            name: "ux_invitations_active_normalized_email",
            schema: "accounts",
            table: "invitations",
            column: "normalized_email",
            unique: true,
            filter: "accepted_at IS NULL AND revoked_at IS NULL");
    }

    protected override void Down(MigrationBuilder migrationBuilder) =>
        migrationBuilder.DropTable("invitations", "accounts");
}