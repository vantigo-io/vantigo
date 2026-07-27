using Microsoft.EntityFrameworkCore.Migrations;

using Npgsql.EntityFrameworkCore.PostgreSQL.Metadata;

#nullable disable

namespace Vantigo.Customers.Api.Database.Migrations
{
    /// <inheritdoc />
    public partial class AddContactsAndCustomersContacts : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.CreateTable(
                name: "contacts",
                columns: table => new
                {
                    id = table.Column<int>(type: "integer", nullable: false, comment: "The unique identifier of the contact")
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    first_name = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: false, comment: "The given name of the contact"),
                    last_name = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: false, comment: "The family name of the contact"),
                    middle_name = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: true, comment: "The middle name of the contact, when known"),
                    prefix = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: true, comment: "An honorific prefix such as 'Dr.', when known"),
                    suffix = table.Column<string>(type: "character varying(20)", maxLength: 20, nullable: true, comment: "An honorific suffix such as 'Jr.' or 'PhD', when known"),
                    phone = table.Column<string>(type: "character varying(30)", unicode: false, maxLength: 30, nullable: true, comment: "The phone number the contact can be reached on, when known"),
                    email = table.Column<string>(type: "character varying(255)", unicode: false, maxLength: 255, nullable: true, comment: "The email address the contact can be reached on, when known")
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_contacts", x => x.id);
                });

            migrationBuilder.CreateTable(
                name: "customers_contacts",
                columns: table => new
                {
                    customer_id = table.Column<int>(type: "integer", nullable: false, comment: "The id of the customer the contact is associated with"),
                    contact_id = table.Column<int>(type: "integer", nullable: false, comment: "The id of the associated contact"),
                    role = table.Column<string>(type: "character varying(255)", maxLength: 255, nullable: false, comment: "The role the contact holds for the customer, such as 'CEO' or 'Custodian'"),
                    phone = table.Column<string>(type: "character varying(30)", unicode: false, maxLength: 30, nullable: true, comment: "A connection-specific phone number, when it differs from the contact's own"),
                    email = table.Column<string>(type: "character varying(255)", unicode: false, maxLength: 255, nullable: true, comment: "A connection-specific email address, when it differs from the contact's own")
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_customers_contacts", x => new { x.customer_id, x.contact_id });
                    table.ForeignKey(
                        name: "FK_customers_contacts_contacts_contact_id",
                        column: x => x.contact_id,
                        principalTable: "contacts",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                    table.ForeignKey(
                        name: "FK_customers_contacts_customers_customer_id",
                        column: x => x.customer_id,
                        principalTable: "customers",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateIndex(
                name: "IX_customers_contacts_contact_id",
                table: "customers_contacts",
                column: "contact_id");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "customers_contacts");

            migrationBuilder.DropTable(
                name: "contacts");
        }
    }
}