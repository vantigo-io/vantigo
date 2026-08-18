using System;

using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Customers.Database.Customers.Migrations
{
    /// <inheritdoc />
    internal partial class AddContactCreatedAt : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AddColumn<DateTimeOffset>(
                name: "created_at",
                schema: "customers",
                table: "contacts",
                type: "timestamp with time zone",
                nullable: false,
                defaultValueSql: "now()",
                comment: "When the contact was first persisted");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropColumn(
                name: "created_at",
                schema: "customers",
                table: "contacts");
        }
    }
}