using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Identity.Database.Accounts.Migrations
{
    /// <inheritdoc />
    public partial class ScimTokenProvenanceRestrictionV2 : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropForeignKey(
                name: "fk_scim_bearer_tokens_connections_id",
                schema: "identity",
                table: "scim_bearer_tokens");

            migrationBuilder.AddForeignKey(
                name: "fk_scim_bearer_tokens_connections_id",
                schema: "identity",
                table: "scim_bearer_tokens",
                column: "scim_connection_id",
                principalSchema: "identity",
                principalTable: "scim_connections",
                principalColumn: "id",
                onDelete: ReferentialAction.Restrict);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropForeignKey(
                name: "fk_scim_bearer_tokens_connections_id",
                schema: "identity",
                table: "scim_bearer_tokens");

            migrationBuilder.AddForeignKey(
                name: "fk_scim_bearer_tokens_connections_id",
                schema: "identity",
                table: "scim_bearer_tokens",
                column: "scim_connection_id",
                principalSchema: "identity",
                principalTable: "scim_connections",
                principalColumn: "id",
                onDelete: ReferentialAction.Cascade);
        }
    }
}