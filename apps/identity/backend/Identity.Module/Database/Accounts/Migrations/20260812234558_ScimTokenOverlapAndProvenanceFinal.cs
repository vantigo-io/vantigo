using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Identity.Database.Accounts.Migrations
{
    /// <inheritdoc />
    public partial class ScimTokenOverlapAndProvenanceFinal : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropForeignKey(
                name: "fk_access_groups_scim_connections_id",
                schema: "identity",
                table: "access_groups");

            migrationBuilder.DropForeignKey(
                name: "fk_scim_connections_federation_connections_id",
                schema: "identity",
                table: "scim_connections");

            migrationBuilder.DropForeignKey(
                name: "fk_scim_user_mappings_connections_id",
                schema: "identity",
                table: "scim_user_mappings");

            migrationBuilder.DropForeignKey(
                name: "fk_scim_user_mappings_users_id",
                schema: "identity",
                table: "scim_user_mappings");

            migrationBuilder.AddForeignKey(
                name: "fk_access_groups_scim_connections_id",
                schema: "identity",
                table: "access_groups",
                column: "scim_connection_id",
                principalSchema: "identity",
                principalTable: "scim_connections",
                principalColumn: "id",
                onDelete: ReferentialAction.Restrict);

            migrationBuilder.AddForeignKey(
                name: "fk_scim_connections_federation_connections_id",
                schema: "identity",
                table: "scim_connections",
                column: "federation_connection_id",
                principalSchema: "identity",
                principalTable: "federation_connections",
                principalColumn: "id",
                onDelete: ReferentialAction.Restrict);

            migrationBuilder.AddForeignKey(
                name: "fk_scim_user_mappings_connections_id",
                schema: "identity",
                table: "scim_user_mappings",
                column: "scim_connection_id",
                principalSchema: "identity",
                principalTable: "scim_connections",
                principalColumn: "id",
                onDelete: ReferentialAction.Restrict);

            migrationBuilder.AddForeignKey(
                name: "fk_scim_user_mappings_users_id",
                schema: "identity",
                table: "scim_user_mappings",
                column: "user_id",
                principalSchema: "identity",
                principalTable: "users",
                principalColumn: "id",
                onDelete: ReferentialAction.Restrict);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropForeignKey(
                name: "fk_access_groups_scim_connections_id",
                schema: "identity",
                table: "access_groups");

            migrationBuilder.DropForeignKey(
                name: "fk_scim_connections_federation_connections_id",
                schema: "identity",
                table: "scim_connections");

            migrationBuilder.DropForeignKey(
                name: "fk_scim_user_mappings_connections_id",
                schema: "identity",
                table: "scim_user_mappings");

            migrationBuilder.DropForeignKey(
                name: "fk_scim_user_mappings_users_id",
                schema: "identity",
                table: "scim_user_mappings");

            migrationBuilder.AddForeignKey(
                name: "fk_access_groups_scim_connections_id",
                schema: "identity",
                table: "access_groups",
                column: "scim_connection_id",
                principalSchema: "identity",
                principalTable: "scim_connections",
                principalColumn: "id",
                onDelete: ReferentialAction.Cascade);

            migrationBuilder.AddForeignKey(
                name: "fk_scim_connections_federation_connections_id",
                schema: "identity",
                table: "scim_connections",
                column: "federation_connection_id",
                principalSchema: "identity",
                principalTable: "federation_connections",
                principalColumn: "id",
                onDelete: ReferentialAction.Cascade);

            migrationBuilder.AddForeignKey(
                name: "fk_scim_user_mappings_connections_id",
                schema: "identity",
                table: "scim_user_mappings",
                column: "scim_connection_id",
                principalSchema: "identity",
                principalTable: "scim_connections",
                principalColumn: "id",
                onDelete: ReferentialAction.Cascade);

            migrationBuilder.AddForeignKey(
                name: "fk_scim_user_mappings_users_id",
                schema: "identity",
                table: "scim_user_mappings",
                column: "user_id",
                principalSchema: "identity",
                principalTable: "users",
                principalColumn: "id",
                onDelete: ReferentialAction.Cascade);
        }
    }
}