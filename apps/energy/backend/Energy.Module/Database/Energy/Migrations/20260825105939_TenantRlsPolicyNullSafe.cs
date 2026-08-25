using Microsoft.EntityFrameworkCore.Migrations;

using Vantigo.Tenancy.EntityFramework;

#nullable disable

namespace Vantigo.Energy.Database.Energy.Migrations
{
    /// <inheritdoc />
    public partial class TenantRlsPolicyNullSafe : Migration
    {
        private static readonly string[] Tables =
        [
            "metering_points",
            "meters",
            "consumption_intervals",
            "supply_periods",
        ];

        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            foreach (var table in Tables)
                migrationBuilder.MakeTenantRlsPolicyNullSafe("energy", table);

            // Partition creation is DDL, which the least-privilege runtime role
            // must not hold; the function runs with its owner's rights instead.
            // The body only interpolates a date via %I/%L, and the pinned
            // search_path keeps definer-rights name resolution safe.
            migrationBuilder.Sql("""
                ALTER FUNCTION energy.ensure_consumption_partition(date)
                    SECURITY DEFINER SET search_path = pg_catalog;
                """);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.Sql("""
                ALTER FUNCTION energy.ensure_consumption_partition(date)
                    SECURITY INVOKER RESET search_path;
                """);

            foreach (var table in Tables)
                migrationBuilder.RevertTenantRlsPolicyNullSafe("energy", table);
        }
    }
}