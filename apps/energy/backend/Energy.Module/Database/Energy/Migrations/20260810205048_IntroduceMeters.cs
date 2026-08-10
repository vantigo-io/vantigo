using System;

using Microsoft.EntityFrameworkCore.Migrations;

using Npgsql.EntityFrameworkCore.PostgreSQL.Metadata;

#nullable disable

namespace Vantigo.Energy.Database.Energy.Migrations
{
    /// <inheritdoc />
    public partial class IntroduceMeters : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.CreateTable(
                name: "meters",
                schema: "energy",
                columns: table => new
                {
                    id = table.Column<int>(type: "integer", nullable: false)
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    metering_point_id = table.Column<int>(type: "integer", nullable: false),
                    meter_number = table.Column<string>(type: "character varying(64)", maxLength: 64, nullable: false),
                    installed_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    removed_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_meters", x => x.id);
                    table.ForeignKey(
                        name: "FK_meters_metering_points_metering_point_id",
                        column: x => x.metering_point_id,
                        principalSchema: "energy",
                        principalTable: "metering_points",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.Sql(
                "INSERT INTO energy.meters (metering_point_id, meter_number, installed_at) " +
                "SELECT id, meter_number, created_at FROM energy.metering_points;");

            migrationBuilder.DropColumn(
                name: "meter_number",
                schema: "energy",
                table: "metering_points");

            migrationBuilder.CreateIndex(
                name: "IX_meters_metering_point_id",
                schema: "energy",
                table: "meters",
                column: "metering_point_id",
                unique: true,
                filter: "removed_at IS NULL");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AddColumn<string>(
                name: "meter_number",
                schema: "energy",
                table: "metering_points",
                type: "character varying(64)",
                maxLength: 64,
                nullable: false,
                defaultValue: "");

            migrationBuilder.Sql(
                "UPDATE energy.metering_points AS point " +
                "SET meter_number = meter.meter_number " +
                "FROM energy.meters AS meter " +
                "WHERE meter.metering_point_id = point.id AND meter.removed_at IS NULL;");

            migrationBuilder.DropTable(
                name: "meters",
                schema: "energy");
        }
    }
}