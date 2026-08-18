using System;

using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Customers.Database.Customers.Migrations
{
    /// <inheritdoc />
    public partial class AddCustomerStatusAndTimestamps : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AddColumn<DateTimeOffset>(
                name: "created_at",
                schema: "customers",
                table: "customers",
                type: "timestamp with time zone",
                nullable: false,
                defaultValueSql: "now()",
                comment: "When the customer was first persisted");

            migrationBuilder.AddColumn<string>(
                name: "status",
                schema: "customers",
                table: "customers",
                type: "character varying(20)",
                unicode: false,
                maxLength: 20,
                nullable: false,
                defaultValue: "active",
                comment: "The lifecycle status of the customer");

            migrationBuilder.AddColumn<DateTimeOffset>(
                name: "updated_at",
                schema: "customers",
                table: "customers",
                type: "timestamp with time zone",
                nullable: false,
                defaultValueSql: "now()",
                comment: "When the customer row itself was last changed");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropColumn(
                name: "created_at",
                schema: "customers",
                table: "customers");

            migrationBuilder.DropColumn(
                name: "status",
                schema: "customers",
                table: "customers");

            migrationBuilder.DropColumn(
                name: "updated_at",
                schema: "customers",
                table: "customers");
        }
    }
}