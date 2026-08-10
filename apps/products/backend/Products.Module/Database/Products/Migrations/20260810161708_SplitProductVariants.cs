using System;
using System.Collections.Generic;

using Microsoft.EntityFrameworkCore.Migrations;

using Npgsql.EntityFrameworkCore.PostgreSQL.Metadata;

#nullable disable

namespace Vantigo.Products.Database.Products.Migrations
{
    /// <inheritdoc />
    public partial class SplitProductVariants : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.CreateTable(
                name: "product_variants",
                schema: "products",
                columns: table => new
                {
                    id = table.Column<int>(type: "integer", nullable: false, comment: "The unique identifier of the product variant")
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    product_id = table.Column<int>(type: "integer", nullable: false, comment: "The product that owns the variant"),
                    sku = table.Column<string>(type: "character varying(64)", unicode: false, maxLength: 64, nullable: false, comment: "The stock keeping unit uniquely identifying this sellable variant"),
                    barcode = table.Column<string>(type: "character varying(14)", unicode: false, maxLength: 14, nullable: true, comment: "The GTIN barcode of the variant, unique when set"),
                    unit = table.Column<string>(type: "character varying(20)", unicode: false, maxLength: 20, nullable: false, comment: "The unit the variant is sold in, for instance pcs or hour"),
                    standard_cost = table.Column<decimal>(type: "numeric(12,2)", precision: 12, scale: 2, nullable: true, comment: "An indicative cost in the company base currency excluding VAT"),
                    weight_kg = table.Column<decimal>(type: "numeric(10,3)", precision: 10, scale: 3, nullable: true, comment: "The gross weight of one unit in kilograms"),
                    length_cm = table.Column<decimal>(type: "numeric(10,1)", precision: 10, scale: 1, nullable: true, comment: "The length of one unit in centimetres"),
                    width_cm = table.Column<decimal>(type: "numeric(10,1)", precision: 10, scale: 1, nullable: true, comment: "The width of one unit in centimetres"),
                    height_cm = table.Column<decimal>(type: "numeric(10,1)", precision: 10, scale: 1, nullable: true, comment: "The height of one unit in centimetres"),
                    option_values = table.Column<Dictionary<string, string>>(type: "jsonb", nullable: false, comment: "The option values distinguishing this variant"),
                    created_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false, comment: "When the variant was created"),
                    updated_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false, comment: "When the variant was last updated")
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_product_variants", x => x.id);
                    table.ForeignKey(
                        name: "FK_product_variants_products_product_id",
                        column: x => x.product_id,
                        principalSchema: "products",
                        principalTable: "products",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            // Preserve each existing product as its own default variant before moving
            // prices and dropping the old sellable columns. This is intentionally raw SQL
            // so the migration remains data-preserving for existing installations.
            migrationBuilder.Sql(
                "INSERT INTO products.product_variants (product_id, sku, barcode, unit, standard_cost, weight_kg, length_cm, width_cm, height_cm, option_values, created_at, updated_at) " +
                "SELECT id, sku, barcode, unit, standard_cost, weight_kg, length_cm, width_cm, height_cm, '{}'::jsonb, created_at, updated_at " +
                "FROM products.products;");

            migrationBuilder.AddColumn<int>(
                name: "variant_id",
                schema: "products",
                table: "product_prices",
                type: "integer",
                nullable: true,
                comment: "The product variant the price belongs to");

            migrationBuilder.Sql(
                "UPDATE products.product_prices AS pp " +
                "SET variant_id = pv.id " +
                "FROM products.product_variants AS pv " +
                "WHERE pv.product_id = pp.product_id;");

            migrationBuilder.DropForeignKey(
                name: "FK_product_prices_products_product_id",
                table: "product_prices",
                schema: "products");

            migrationBuilder.DropIndex(
                name: "IX_product_prices_product_id_currency",
                table: "product_prices",
                schema: "products");

            migrationBuilder.AlterColumn<int>(
                name: "variant_id",
                schema: "products",
                table: "product_prices",
                type: "integer",
                nullable: false,
                oldClrType: typeof(int),
                oldType: "integer",
                oldNullable: true,
                oldComment: "The product variant the price belongs to",
                comment: "The product variant the price belongs to");

            migrationBuilder.CreateIndex(
                name: "IX_product_prices_variant_id_currency",
                table: "product_prices",
                columns: new[] { "variant_id", "currency" },
                schema: "products");

            migrationBuilder.AddForeignKey(
                name: "FK_product_prices_product_variants_variant_id",
                table: "product_prices",
                column: "variant_id",
                schema: "products",
                principalSchema: "products",
                principalTable: "product_variants",
                principalColumn: "id",
                onDelete: ReferentialAction.Cascade);

            migrationBuilder.DropColumn(
                name: "product_id",
                table: "product_prices",
                schema: "products");

            migrationBuilder.DropIndex(name: "IX_products_barcode", table: "products", schema: "products");
            migrationBuilder.DropIndex(name: "IX_products_sku", table: "products", schema: "products");

            migrationBuilder.DropColumn(name: "barcode", table: "products", schema: "products");
            migrationBuilder.DropColumn(name: "height_cm", table: "products", schema: "products");
            migrationBuilder.DropColumn(name: "length_cm", table: "products", schema: "products");
            migrationBuilder.DropColumn(name: "sku", table: "products", schema: "products");
            migrationBuilder.DropColumn(name: "standard_cost", table: "products", schema: "products");
            migrationBuilder.DropColumn(name: "unit", table: "products", schema: "products");
            migrationBuilder.DropColumn(name: "weight_kg", table: "products", schema: "products");
            migrationBuilder.DropColumn(name: "width_cm", table: "products", schema: "products");

            migrationBuilder.CreateIndex(name: "IX_product_variants_barcode", table: "product_variants", column: "barcode", unique: true, filter: "barcode IS NOT NULL", schema: "products");
            migrationBuilder.CreateIndex(name: "IX_product_variants_product_id", table: "product_variants", column: "product_id", schema: "products");
            migrationBuilder.CreateIndex(name: "IX_product_variants_sku", table: "product_variants", column: "sku", unique: true, schema: "products");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            // Down is schema-only and intentionally lossy: product-level values cannot
            // faithfully restore multiple variants into one product row.
            migrationBuilder.DropForeignKey(name: "FK_product_prices_product_variants_variant_id", table: "product_prices", schema: "products");
            migrationBuilder.DropIndex(name: "IX_product_prices_variant_id_currency", table: "product_prices", schema: "products");
            migrationBuilder.DropIndex(name: "IX_product_variants_barcode", table: "product_variants", schema: "products");
            migrationBuilder.DropIndex(name: "IX_product_variants_product_id", table: "product_variants", schema: "products");
            migrationBuilder.DropIndex(name: "IX_product_variants_sku", table: "product_variants", schema: "products");

            migrationBuilder.AddColumn<int>(name: "product_id", table: "product_prices", schema: "products", type: "integer", nullable: true, comment: "The product the price belongs to");
            migrationBuilder.Sql("UPDATE products.product_prices AS pp SET product_id = pv.product_id FROM products.product_variants AS pv WHERE pv.id = pp.variant_id;");
            migrationBuilder.DropColumn(name: "variant_id", table: "product_prices", schema: "products");
            migrationBuilder.CreateIndex(name: "IX_product_prices_product_id_currency", table: "product_prices", columns: new[] { "product_id", "currency" }, schema: "products");

            migrationBuilder.AddColumn<string>(name: "barcode", table: "products", schema: "products", type: "character varying(14)", unicode: false, maxLength: 14, nullable: true);
            migrationBuilder.AddColumn<decimal>(name: "height_cm", table: "products", schema: "products", type: "numeric(10,1)", precision: 10, scale: 1, nullable: true);
            migrationBuilder.AddColumn<decimal>(name: "length_cm", table: "products", schema: "products", type: "numeric(10,1)", precision: 10, scale: 1, nullable: true);
            migrationBuilder.AddColumn<string>(name: "sku", table: "products", schema: "products", type: "character varying(64)", unicode: false, maxLength: 64, nullable: false, defaultValue: "");
            migrationBuilder.AddColumn<decimal>(name: "standard_cost", table: "products", schema: "products", type: "numeric(12,2)", precision: 12, scale: 2, nullable: true);
            migrationBuilder.AddColumn<string>(name: "unit", table: "products", schema: "products", type: "character varying(20)", unicode: false, maxLength: 20, nullable: false, defaultValue: "pcs");
            migrationBuilder.AddColumn<decimal>(name: "weight_kg", table: "products", schema: "products", type: "numeric(10,3)", precision: 10, scale: 3, nullable: true);
            migrationBuilder.AddColumn<decimal>(name: "width_cm", table: "products", schema: "products", type: "numeric(10,1)", precision: 10, scale: 1, nullable: true);
            migrationBuilder.CreateIndex(name: "IX_products_barcode", table: "products", column: "barcode", unique: true, filter: "barcode IS NOT NULL", schema: "products");
            migrationBuilder.CreateIndex(name: "IX_products_sku", table: "products", column: "sku", unique: true, schema: "products");
            migrationBuilder.AddForeignKey(name: "FK_product_prices_products_product_id", table: "product_prices", column: "product_id", principalTable: "products", principalColumn: "id", onDelete: ReferentialAction.Cascade, schema: "products", principalSchema: "products");
            migrationBuilder.DropTable(name: "product_variants", schema: "products");
        }
    }
}