using System;

using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Identity.Database.Accounts.Migrations
{
    /// <inheritdoc />
    public partial class PasskeyCeremonyHardening : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AlterColumn<Guid>(
                name: "user_id",
                schema: "identity",
                table: "passkey_ceremonies",
                type: "uuid",
                nullable: true,
                oldClrType: typeof(Guid),
                oldType: "uuid");

            migrationBuilder.AddColumn<string>(
                name: "client_address",
                schema: "identity",
                table: "passkey_ceremonies",
                type: "character varying(64)",
                maxLength: 64,
                nullable: true);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropColumn(
                name: "client_address",
                schema: "identity",
                table: "passkey_ceremonies");

            migrationBuilder.AlterColumn<Guid>(
                name: "user_id",
                schema: "identity",
                table: "passkey_ceremonies",
                type: "uuid",
                nullable: false,
                defaultValue: new Guid("00000000-0000-0000-0000-000000000000"),
                oldClrType: typeof(Guid),
                oldType: "uuid",
                oldNullable: true);
        }
    }
}