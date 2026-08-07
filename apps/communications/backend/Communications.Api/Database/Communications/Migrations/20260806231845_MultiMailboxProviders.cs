using System;

using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Communications.Api.Database.Communications.Migrations
{
    /// <inheritdoc />
    public partial class MultiMailboxProviders : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AddColumn<bool>(
                name: "IsDefault",
                schema: "communications",
                table: "shared_mailboxes",
                type: "boolean",
                nullable: false,
                defaultValue: false);

            migrationBuilder.AddColumn<string>(
                name: "Provider",
                schema: "communications",
                table: "shared_mailboxes",
                type: "character varying(20)",
                maxLength: 20,
                nullable: false,
                defaultValue: "smtp");

            migrationBuilder.CreateTable(
                name: "mailbox_provider_credentials",
                schema: "communications",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uuid", nullable: false),
                    MailboxId = table.Column<Guid>(type: "uuid", nullable: false),
                    Provider = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: false),
                    SettingsJson = table.Column<string>(type: "text", nullable: false),
                    SecretCiphertext = table.Column<string>(type: "text", nullable: false),
                    CreatedAt = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_mailbox_provider_credentials", x => x.Id);
                    table.ForeignKey(
                        name: "FK_mailbox_provider_credentials_shared_mailboxes_MailboxId",
                        column: x => x.MailboxId,
                        principalSchema: "communications",
                        principalTable: "shared_mailboxes",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateIndex(
                name: "IX_mailbox_provider_credentials_MailboxId",
                schema: "communications",
                table: "mailbox_provider_credentials",
                column: "MailboxId",
                unique: true);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "mailbox_provider_credentials",
                schema: "communications");

            migrationBuilder.DropColumn(
                name: "IsDefault",
                schema: "communications",
                table: "shared_mailboxes");

            migrationBuilder.DropColumn(
                name: "Provider",
                schema: "communications",
                table: "shared_mailboxes");
        }
    }
}