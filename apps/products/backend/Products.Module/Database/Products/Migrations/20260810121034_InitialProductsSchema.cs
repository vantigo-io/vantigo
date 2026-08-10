using System;

using Microsoft.EntityFrameworkCore.Migrations;

using Npgsql.EntityFrameworkCore.PostgreSQL.Metadata;

#nullable disable

namespace Vantigo.Products.Database.Products.Migrations
{
    /// <inheritdoc />
    public partial class InitialProductsSchema : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.EnsureSchema(
                name: "products");

            migrationBuilder.CreateTable(
                name: "product_categories",
                schema: "products",
                columns: table => new
                {
                    id = table.Column<int>(type: "integer", nullable: false, comment: "The unique identifier of the category")
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    name = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false, comment: "The display name of the category, unique among its siblings"),
                    parent_id = table.Column<int>(type: "integer", nullable: true, comment: "The parent category; null means the category is a root")
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_product_categories", x => x.id);
                    table.ForeignKey(
                        name: "FK_product_categories_product_categories_parent_id",
                        column: x => x.parent_id,
                        principalSchema: "products",
                        principalTable: "product_categories",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Restrict);
                });

            migrationBuilder.CreateTable(
                name: "products",
                schema: "products",
                columns: table => new
                {
                    id = table.Column<int>(type: "integer", nullable: false, comment: "The unique identifier of the product")
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    name = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false, comment: "The display name of the product"),
                    sku = table.Column<string>(type: "character varying(64)", unicode: false, maxLength: 64, nullable: false, comment: "The stock keeping unit uniquely identifying this sellable unit"),
                    description = table.Column<string>(type: "character varying(4000)", maxLength: 4000, nullable: true, comment: "A free-form plain-text description of the product"),
                    category_id = table.Column<int>(type: "integer", nullable: true, comment: "The category the product belongs to; null means uncategorised"),
                    barcode = table.Column<string>(type: "character varying(14)", unicode: false, maxLength: 14, nullable: true, comment: "The GTIN barcode (GTIN-8/12/13/14) of the product, unique when set"),
                    type = table.Column<string>(type: "character varying(20)", unicode: false, maxLength: 20, nullable: false, comment: "Whether the product is a physical good or a performed service"),
                    status = table.Column<string>(type: "character varying(20)", unicode: false, maxLength: 20, nullable: false, comment: "The lifecycle status of the product"),
                    unit = table.Column<string>(type: "character varying(20)", unicode: false, maxLength: 20, nullable: false, comment: "The unit the product is sold in, for instance pcs or hour"),
                    standard_cost = table.Column<decimal>(type: "numeric(12,2)", precision: 12, scale: 2, nullable: true, comment: "An indicative cost in the company base currency excluding VAT, used for margin estimates"),
                    vat_rate = table.Column<decimal>(type: "numeric(5,4)", precision: 5, scale: 4, nullable: false, comment: "The VAT rate applied when the product is sold, for instance 0.25 for 25%"),
                    weight_kg = table.Column<decimal>(type: "numeric(10,3)", precision: 10, scale: 3, nullable: true, comment: "The gross weight of one unit in kilograms"),
                    length_cm = table.Column<decimal>(type: "numeric(10,1)", precision: 10, scale: 1, nullable: true, comment: "The length of one unit in centimetres"),
                    width_cm = table.Column<decimal>(type: "numeric(10,1)", precision: 10, scale: 1, nullable: true, comment: "The width of one unit in centimetres"),
                    height_cm = table.Column<decimal>(type: "numeric(10,1)", precision: 10, scale: 1, nullable: true, comment: "The height of one unit in centimetres"),
                    created_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false, comment: "When the product was created"),
                    updated_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false, comment: "When the product was last updated")
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_products", x => x.id);
                    table.ForeignKey(
                        name: "FK_products_product_categories_category_id",
                        column: x => x.category_id,
                        principalSchema: "products",
                        principalTable: "product_categories",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Restrict);
                });

            migrationBuilder.CreateTable(
                name: "product_prices",
                schema: "products",
                columns: table => new
                {
                    id = table.Column<int>(type: "integer", nullable: false, comment: "The unique identifier of the price")
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    product_id = table.Column<int>(type: "integer", nullable: false, comment: "The product the price belongs to"),
                    currency = table.Column<string>(type: "character(3)", unicode: false, fixedLength: true, maxLength: 3, nullable: false, comment: "The ISO 4217 currency code of the price"),
                    amount = table.Column<decimal>(type: "numeric(12,2)", precision: 12, scale: 2, nullable: false, comment: "The price amount in the given currency, excluding VAT"),
                    valid_from = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true, comment: "When the price becomes valid; null means valid from the beginning of time"),
                    valid_to = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true, comment: "When the price stops being valid (exclusive); null means open-ended")
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_product_prices", x => x.id);
                    table.ForeignKey(
                        name: "FK_product_prices_products_product_id",
                        column: x => x.product_id,
                        principalSchema: "products",
                        principalTable: "products",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateIndex(
                name: "IX_product_categories_parent_id_name",
                schema: "products",
                table: "product_categories",
                columns: new[] { "parent_id", "name" },
                unique: true)
                .Annotation("Npgsql:NullsDistinct", false);

            migrationBuilder.CreateIndex(
                name: "IX_product_prices_product_id_currency",
                schema: "products",
                table: "product_prices",
                columns: new[] { "product_id", "currency" });

            migrationBuilder.CreateIndex(
                name: "IX_products_barcode",
                schema: "products",
                table: "products",
                column: "barcode",
                unique: true,
                filter: "barcode IS NOT NULL");

            migrationBuilder.CreateIndex(
                name: "IX_products_category_id",
                schema: "products",
                table: "products",
                column: "category_id");

            migrationBuilder.CreateIndex(
                name: "IX_products_sku",
                schema: "products",
                table: "products",
                column: "sku",
                unique: true);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "product_prices",
                schema: "products");

            migrationBuilder.DropTable(
                name: "products",
                schema: "products");

            migrationBuilder.DropTable(
                name: "product_categories",
                schema: "products");
        }
    }
}