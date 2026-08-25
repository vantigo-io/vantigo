using Microsoft.EntityFrameworkCore.Migrations;

using Vantigo.Tenancy.EntityFramework;

#nullable disable

namespace Vantigo.Customers.Database.Customers.Migrations
{
    /// <inheritdoc />
    public partial class TenantRlsPolicyNullSafe : Migration
    {
        private static readonly string[] Tables =
        [
            "contacts",
            "customers",
            "customers_contacts",
            "customers_timeline_entries",
            "customers_timeline_entries_revisions",
            "tenant_counters",
        ];

        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            foreach (var table in Tables)
                migrationBuilder.MakeTenantRlsPolicyNullSafe("customers", table);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            foreach (var table in Tables)
                migrationBuilder.RevertTenantRlsPolicyNullSafe("customers", table);
        }
    }
}