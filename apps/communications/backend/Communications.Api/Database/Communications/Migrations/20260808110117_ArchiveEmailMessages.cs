using System;

using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Communications.Api.Database.Communications.Migrations
{
    /// <inheritdoc />
    public partial class ArchiveEmailMessages : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AddColumn<DateTimeOffset>(
                name: "ArchivedAt",
                schema: "communications",
                table: "email_messages",
                type: "timestamp with time zone",
                nullable: true);

            migrationBuilder.CreateIndex(
                name: "IX_email_messages_ArchivedAt",
                schema: "communications",
                table: "email_messages",
                column: "ArchivedAt");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropIndex(
                name: "IX_email_messages_ArchivedAt",
                schema: "communications",
                table: "email_messages");

            migrationBuilder.DropColumn(
                name: "ArchivedAt",
                schema: "communications",
                table: "email_messages");
        }
    }
}