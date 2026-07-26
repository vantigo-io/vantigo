using Microsoft.EntityFrameworkCore.Migrations;
using Npgsql.EntityFrameworkCore.PostgreSQL.Metadata;

#nullable disable

namespace Vantigo.Customers.Api.Database.Migrations
{
    /// <inheritdoc />
    public partial class AddCustomersTable : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.CreateTable(
                name: "customers",
                columns: table => new
                {
                    id = table.Column<int>(type: "integer", nullable: false, comment: "The unique identifier of the customer")
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    name = table.Column<string>(type: "character varying(255)", maxLength: 255, nullable: false, comment: "The friendly name of the customer"),
                    legal_country = table.Column<string>(type: "character varying(2)", unicode: false, maxLength: 2, nullable: true, comment: "The legal country of the identity that is associated with the customer"),
                    legal_id = table.Column<string>(type: "character varying(50)", unicode: false, maxLength: 50, nullable: true, comment: "The legal id of the identity that is associated with the customer"),
                    legal_name = table.Column<string>(type: "character varying(255)", maxLength: 255, nullable: true, comment: "The legal name of the identity that is associated with the customer"),
                    legal_type = table.Column<string>(type: "character varying(50)", unicode: false, maxLength: 50, nullable: true, comment: "The legal type of the identity that is associated with the customer")
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_customers", x => x.id);
                });
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "customers");
        }
    }
}
