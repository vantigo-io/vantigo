using System;

using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Identity.Database.Accounts.Migrations
{
    /// <inheritdoc />
    public partial class FederatedDirectoryClaims : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AddColumn<Guid>(
                name: "directory_object_id",
                schema: "identity",
                table: "federated_identities",
                type: "uuid",
                nullable: true);

            migrationBuilder.AddColumn<string>(
                name: "directory_tenant_id",
                schema: "identity",
                table: "federated_identities",
                type: "character varying(128)",
                maxLength: 128,
                nullable: true);

            migrationBuilder.CreateIndex(
                name: "ux_federated_identities_connection_directory_identity",
                schema: "identity",
                table: "federated_identities",
                columns: new[] { "connection_id", "directory_tenant_id", "directory_object_id" },
                unique: true,
                filter: "directory_tenant_id IS NOT NULL AND directory_object_id IS NOT NULL");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropIndex(
                name: "ux_federated_identities_connection_directory_identity",
                schema: "identity",
                table: "federated_identities");

            migrationBuilder.DropColumn(
                name: "directory_object_id",
                schema: "identity",
                table: "federated_identities");

            migrationBuilder.DropColumn(
                name: "directory_tenant_id",
                schema: "identity",
                table: "federated_identities");
        }
    }
}