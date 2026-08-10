using System;

using Microsoft.EntityFrameworkCore.Migrations;

using Npgsql.EntityFrameworkCore.PostgreSQL.Metadata;

#nullable disable

namespace Vantigo.Energy.Database.Energy.Migrations
{
    /// <inheritdoc />
    public partial class InitialEnergySchema : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.EnsureSchema(
                name: "energy");

            migrationBuilder.Sql("CREATE EXTENSION IF NOT EXISTS btree_gist;");

            migrationBuilder.CreateTable(
                name: "metering_points",
                schema: "energy",
                columns: table => new
                {
                    id = table.Column<int>(type: "integer", nullable: false)
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    gsrn = table.Column<string>(type: "character varying(18)", unicode: false, maxLength: 18, nullable: false),
                    meter_number = table.Column<string>(type: "character varying(64)", maxLength: 64, nullable: false),
                    street_address = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false),
                    postal_code = table.Column<string>(type: "character varying(16)", maxLength: 16, nullable: false),
                    city = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: false),
                    country_code = table.Column<string>(type: "character varying(2)", unicode: false, maxLength: 2, nullable: false),
                    price_area = table.Column<string>(type: "character varying(4)", unicode: false, maxLength: 4, nullable: false),
                    grid_area = table.Column<string>(type: "character varying(64)", unicode: false, maxLength: 64, nullable: true),
                    expected_annual_consumption_kwh = table.Column<decimal>(type: "numeric(14,3)", precision: 14, scale: 3, nullable: true),
                    latitude = table.Column<double>(type: "double precision", precision: 9, scale: 6, nullable: true),
                    longitude = table.Column<double>(type: "double precision", precision: 9, scale: 6, nullable: true),
                    connection_status = table.Column<string>(type: "character varying(20)", unicode: false, maxLength: 20, nullable: false),
                    created_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    updated_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_metering_points", x => x.id);
                });

            migrationBuilder.CreateTable(
                name: "consumption_intervals",
                schema: "energy",
                columns: table => new
                {
                    id = table.Column<long>(type: "bigint", nullable: false)
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    metering_point_id = table.Column<int>(type: "integer", nullable: false),
                    start = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    end = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    quantity_kwh = table.Column<decimal>(type: "numeric(14,3)", precision: 14, scale: 3, nullable: false),
                    quality = table.Column<string>(type: "character varying(20)", unicode: false, maxLength: 20, nullable: false),
                    source = table.Column<string>(type: "character varying(20)", unicode: false, maxLength: 20, nullable: false),
                    received_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    is_current = table.Column<bool>(type: "boolean", nullable: false),
                    supersedes_id = table.Column<long>(type: "bigint", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_consumption_intervals", x => x.id);
                    table.ForeignKey(
                        name: "FK_consumption_intervals_consumption_intervals_supersedes_id",
                        column: x => x.supersedes_id,
                        principalSchema: "energy",
                        principalTable: "consumption_intervals",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Restrict);
                    table.ForeignKey(
                        name: "FK_consumption_intervals_metering_points_metering_point_id",
                        column: x => x.metering_point_id,
                        principalSchema: "energy",
                        principalTable: "metering_points",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "supply_periods",
                schema: "energy",
                columns: table => new
                {
                    id = table.Column<int>(type: "integer", nullable: false)
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    metering_point_id = table.Column<int>(type: "integer", nullable: false),
                    customer_id = table.Column<int>(type: "integer", nullable: false),
                    start = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    end = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true),
                    status = table.Column<string>(type: "character varying(20)", unicode: false, maxLength: 20, nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_supply_periods", x => x.id);
                    table.ForeignKey(
                        name: "FK_supply_periods_metering_points_metering_point_id",
                        column: x => x.metering_point_id,
                        principalSchema: "energy",
                        principalTable: "metering_points",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateIndex(
                name: "IX_consumption_intervals_metering_point_id_start_end",
                schema: "energy",
                table: "consumption_intervals",
                columns: new[] { "metering_point_id", "start", "end" },
                unique: true,
                filter: "is_current = TRUE");

            migrationBuilder.CreateIndex(
                name: "IX_consumption_intervals_supersedes_id",
                schema: "energy",
                table: "consumption_intervals",
                column: "supersedes_id");

            migrationBuilder.CreateIndex(
                name: "IX_metering_points_gsrn",
                schema: "energy",
                table: "metering_points",
                column: "gsrn",
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_supply_periods_metering_point_id",
                schema: "energy",
                table: "supply_periods",
                column: "metering_point_id");

            migrationBuilder.Sql("ALTER TABLE energy.supply_periods ADD CONSTRAINT supply_periods_no_overlap EXCLUDE USING gist (metering_point_id WITH =, tstzrange(start, COALESCE(\"end\", 'infinity'::timestamptz), '[)') WITH &&) WHERE (status <> 'Cancelled');");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "consumption_intervals",
                schema: "energy");

            migrationBuilder.DropTable(
                name: "supply_periods",
                schema: "energy");

            migrationBuilder.DropTable(
                name: "metering_points",
                schema: "energy");
        }
    }
}