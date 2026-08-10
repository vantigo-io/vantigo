using System;

using Microsoft.EntityFrameworkCore.Migrations;

using Npgsql.EntityFrameworkCore.PostgreSQL.Metadata;

#nullable disable

namespace Vantigo.Products.Database.Products.Migrations
{
    /// <inheritdoc />
    public partial class IntroduceTaxCategories : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.CreateTable(
                name: "tax_categories",
                schema: "products",
                columns: table => new
                {
                    id = table.Column<int>(type: "integer", nullable: false, comment: "The unique identifier of the tax category")
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    name = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: false, comment: "The unique display name of the tax category"),
                    kind = table.Column<string>(type: "character varying(20)", unicode: false, maxLength: 20, nullable: false, comment: "The tax treatment kind"),
                    rate = table.Column<decimal>(type: "numeric(5,4)", precision: 5, scale: 4, nullable: false, comment: "The tax rate as a fraction, for example 0.25 for 25%"),
                    created_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false, comment: "When the tax category was created"),
                    updated_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false, comment: "When the tax category was last updated")
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_tax_categories", x => x.id);
                });

            // Preserve each distinct existing VAT rate as a centrally managed category.
            // INSERT ... SELECT is naturally empty-safe for an empty products table.
            migrationBuilder.Sql(
                "INSERT INTO products.tax_categories (name, kind, rate, created_at, updated_at) " +
                "SELECT 'VAT ' || (vat_rate * 100)::text || '%', " +
                "CASE WHEN vat_rate = 0 THEN 'Zero' ELSE 'Standard' END, " +
                "vat_rate, now(), now() " +
                "FROM (SELECT DISTINCT vat_rate FROM products.products) AS rates;");

            migrationBuilder.AddColumn<int>(
                name: "tax_category_id",
                schema: "products",
                table: "products",
                type: "integer",
                nullable: true,
                comment: "The tax category applied when the product is sold");

            migrationBuilder.Sql(
                "UPDATE products.products AS product " +
                "SET tax_category_id = category.id " +
                "FROM products.tax_categories AS category " +
                "WHERE category.rate = product.vat_rate;");

            migrationBuilder.AlterColumn<int>(
                name: "tax_category_id",
                schema: "products",
                table: "products",
                type: "integer",
                nullable: false,
                oldClrType: typeof(int),
                oldType: "integer",
                oldNullable: true,
                comment: "The tax category applied when the product is sold");

            migrationBuilder.DropColumn(
                name: "vat_rate",
                schema: "products",
                table: "products");

            migrationBuilder.CreateIndex(
                name: "IX_products_tax_category_id",
                schema: "products",
                table: "products",
                column: "tax_category_id");

            migrationBuilder.CreateIndex(
                name: "IX_tax_categories_name",
                schema: "products",
                table: "tax_categories",
                column: "name",
                unique: true);

            migrationBuilder.AddForeignKey(
                name: "FK_products_tax_categories_tax_category_id",
                schema: "products",
                table: "products",
                column: "tax_category_id",
                principalSchema: "products",
                principalTable: "tax_categories",
                principalColumn: "id",
                onDelete: ReferentialAction.Restrict);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropForeignKey(
                name: "FK_products_tax_categories_tax_category_id",
                schema: "products",
                table: "products");

            migrationBuilder.DropTable(
                name: "tax_categories",
                schema: "products");

            migrationBuilder.DropIndex(
                name: "IX_products_tax_category_id",
                schema: "products",
                table: "products");

            migrationBuilder.DropColumn(
                name: "tax_category_id",
                schema: "products",
                table: "products");

            // Down is schema-only and intentionally does not attempt to restore
            // product VAT values from the deleted categories.
            migrationBuilder.AddColumn<decimal>(
                name: "vat_rate",
                schema: "products",
                table: "products",
                type: "numeric(5,4)",
                precision: 5,
                scale: 4,
                nullable: false,
                defaultValue: 0m,
                comment: "The VAT rate applied when the product is sold, for instance 0.25 for 25%");
        }
    }
}