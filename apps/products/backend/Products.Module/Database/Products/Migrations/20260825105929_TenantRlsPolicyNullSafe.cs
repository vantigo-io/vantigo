using Microsoft.EntityFrameworkCore.Migrations;

using Vantigo.Tenancy.EntityFramework;

#nullable disable

namespace Vantigo.Products.Database.Products.Migrations
{
    /// <inheritdoc />
    public partial class TenantRlsPolicyNullSafe : Migration
    {
        private static readonly string[] Tables =
        [
            "product_categories",
            "tax_categories",
            "products",
            "product_variants",
            "product_prices",
        ];

        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            foreach (var table in Tables)
                migrationBuilder.MakeTenantRlsPolicyNullSafe("products", table);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            foreach (var table in Tables)
                migrationBuilder.RevertTenantRlsPolicyNullSafe("products", table);
        }
    }
}