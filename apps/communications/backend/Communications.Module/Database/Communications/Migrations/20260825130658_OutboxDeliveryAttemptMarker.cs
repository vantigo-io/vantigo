using System;

using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Communications.Database.Communications.Migrations
{
    /// <inheritdoc />
    public partial class OutboxDeliveryAttemptMarker : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AddColumn<DateTimeOffset>(
                name: "DeliveryAttemptedAt",
                schema: "communications",
                table: "outbox_jobs",
                type: "timestamp with time zone",
                nullable: true);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropColumn(
                name: "DeliveryAttemptedAt",
                schema: "communications",
                table: "outbox_jobs");
        }
    }
}