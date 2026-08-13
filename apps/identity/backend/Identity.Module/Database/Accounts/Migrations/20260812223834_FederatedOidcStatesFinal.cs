using System;

using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Identity.Database.Accounts.Migrations
{
    /// <inheritdoc />
    public partial class FederatedOidcStatesFinal : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.CreateTable(
                name: "federation_oidc_states",
                schema: "identity",
                columns: table => new
                {
                    state_id = table.Column<Guid>(type: "uuid", nullable: false),
                    state_hash = table.Column<string>(type: "character varying(64)", maxLength: 64, nullable: false),
                    issued_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    expires_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    consumed_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_federation_oidc_states", x => x.state_id);
                });

            migrationBuilder.CreateIndex(
                name: "ix_federation_oidc_states_expires_at",
                schema: "identity",
                table: "federation_oidc_states",
                column: "expires_at");

            migrationBuilder.CreateIndex(
                name: "ux_federation_oidc_states_hash",
                schema: "identity",
                table: "federation_oidc_states",
                column: "state_hash",
                unique: true);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "federation_oidc_states",
                schema: "identity");
        }
    }
}