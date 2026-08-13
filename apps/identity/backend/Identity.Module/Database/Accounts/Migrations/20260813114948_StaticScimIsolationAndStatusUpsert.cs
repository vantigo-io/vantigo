using System;

using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Identity.Database.Accounts.Migrations
{
    /// <inheritdoc />
    public partial class StaticScimIsolationAndStatusUpsert : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.Sql("DELETE FROM identity.operational_events a USING identity.operational_events b WHERE a.kind = b.kind AND a.id < b.id;");

            migrationBuilder.DropIndex(
                name: "ux_scim_connections_federation_connection_id",
                schema: "identity",
                table: "scim_connections");

            migrationBuilder.DropIndex(
                name: "ix_operational_events_kind",
                schema: "identity",
                table: "operational_events");

            migrationBuilder.DropIndex(
                name: "ix_operational_events_kind_occurred_at",
                schema: "identity",
                table: "operational_events");

            migrationBuilder.AlterColumn<Guid>(
                name: "federation_connection_id",
                schema: "identity",
                table: "scim_connections",
                type: "uuid",
                nullable: true,
                oldClrType: typeof(Guid),
                oldType: "uuid");

            migrationBuilder.AddColumn<bool>(
                name: "is_static",
                schema: "identity",
                table: "scim_connections",
                type: "boolean",
                nullable: false,
                defaultValue: false);

            migrationBuilder.CreateIndex(
                name: "ux_scim_connections_federation_connection_id",
                schema: "identity",
                table: "scim_connections",
                column: "federation_connection_id",
                unique: true,
                filter: "federation_connection_id IS NOT NULL");

            migrationBuilder.CreateIndex(
                name: "ux_scim_connections_static",
                schema: "identity",
                table: "scim_connections",
                column: "is_static",
                unique: true,
                filter: "is_static = true");

            migrationBuilder.CreateIndex(
                name: "ux_operational_events_kind",
                schema: "identity",
                table: "operational_events",
                column: "kind",
                unique: true);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropIndex(
                name: "ux_scim_connections_federation_connection_id",
                schema: "identity",
                table: "scim_connections");

            migrationBuilder.DropIndex(
                name: "ux_scim_connections_static",
                schema: "identity",
                table: "scim_connections");

            migrationBuilder.DropIndex(
                name: "ux_operational_events_kind",
                schema: "identity",
                table: "operational_events");

            migrationBuilder.DropColumn(
                name: "is_static",
                schema: "identity",
                table: "scim_connections");

            migrationBuilder.AlterColumn<Guid>(
                name: "federation_connection_id",
                schema: "identity",
                table: "scim_connections",
                type: "uuid",
                nullable: false,
                defaultValue: new Guid("00000000-0000-0000-0000-000000000000"),
                oldClrType: typeof(Guid),
                oldType: "uuid",
                oldNullable: true);

            migrationBuilder.CreateIndex(
                name: "ux_scim_connections_federation_connection_id",
                schema: "identity",
                table: "scim_connections",
                column: "federation_connection_id",
                unique: true);

            migrationBuilder.CreateIndex(
                name: "ix_operational_events_kind",
                schema: "identity",
                table: "operational_events",
                column: "kind");

            migrationBuilder.CreateIndex(
                name: "ix_operational_events_kind_occurred_at",
                schema: "identity",
                table: "operational_events",
                columns: new[] { "kind", "occurred_at" });
        }
    }
}