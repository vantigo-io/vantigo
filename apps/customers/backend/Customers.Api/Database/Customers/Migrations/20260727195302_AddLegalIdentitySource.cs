using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Customers.Api.Database.Customers.Migrations
{
    /// <inheritdoc />
    public partial class AddLegalIdentitySource : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AddColumn<string>(
                name: "legal_source",
                table: "customers",
                type: "character varying(50)",
                unicode: false,
                maxLength: 50,
                nullable: true,
                comment: "Where the legal identity data of the customer was retrieved from");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropColumn(
                name: "legal_source",
                table: "customers");
        }
    }
}