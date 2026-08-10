using Microsoft.EntityFrameworkCore.Migrations;

using Npgsql.EntityFrameworkCore.PostgreSQL.Metadata;

#nullable disable

namespace Vantigo.Products.Api.Database.Products.Migrations
{
    /// <inheritdoc />
    public partial class AddCatalogFields : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AddColumn<string>(
                name: "barcode",
                table: "products",
                type: "character varying(14)",
                unicode: false,
                maxLength: 14,
                nullable: true,
                comment: "The GTIN barcode (GTIN-8/12/13/14) of the product, unique when set");

            migrationBuilder.AddColumn<int>(
                name: "category_id",
                table: "products",
                type: "integer",
                nullable: true,
                comment: "The category the product belongs to; null means uncategorised");

            migrationBuilder.AddColumn<string>(
                name: "description",
                table: "products",
                type: "character varying(4000)",
                maxLength: 4000,
                nullable: true,
                comment: "A free-form plain-text description of the product");

            migrationBuilder.AddColumn<decimal>(
                name: "height_cm",
                table: "products",
                type: "numeric(10,1)",
                precision: 10,
                scale: 1,
                nullable: true,
                comment: "The height of one unit in centimetres");

            migrationBuilder.AddColumn<decimal>(
                name: "length_cm",
                table: "products",
                type: "numeric(10,1)",
                precision: 10,
                scale: 1,
                nullable: true,
                comment: "The length of one unit in centimetres");

            migrationBuilder.AddColumn<decimal>(
                name: "weight_kg",
                table: "products",
                type: "numeric(10,3)",
                precision: 10,
                scale: 3,
                nullable: true,
                comment: "The gross weight of one unit in kilograms");

            migrationBuilder.AddColumn<decimal>(
                name: "width_cm",
                table: "products",
                type: "numeric(10,1)",
                precision: 10,
                scale: 1,
                nullable: true,
                comment: "The width of one unit in centimetres");

            migrationBuilder.CreateTable(
                name: "product_categories",
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
                        principalTable: "product_categories",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Restrict);
                });

            migrationBuilder.CreateIndex(
                name: "IX_products_barcode",
                table: "products",
                column: "barcode",
                unique: true,
                filter: "barcode IS NOT NULL");

            migrationBuilder.CreateIndex(
                name: "IX_products_category_id",
                table: "products",
                column: "category_id");

            migrationBuilder.CreateIndex(
                name: "IX_product_categories_parent_id_name",
                table: "product_categories",
                columns: new[] { "parent_id", "name" },
                unique: true)
                .Annotation("Npgsql:NullsDistinct", false);

            migrationBuilder.AddForeignKey(
                name: "FK_products_product_categories_category_id",
                table: "products",
                column: "category_id",
                principalTable: "product_categories",
                principalColumn: "id",
                onDelete: ReferentialAction.Restrict);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropForeignKey(
                name: "FK_products_product_categories_category_id",
                table: "products");

            migrationBuilder.DropTable(
                name: "product_categories");

            migrationBuilder.DropIndex(
                name: "IX_products_barcode",
                table: "products");

            migrationBuilder.DropIndex(
                name: "IX_products_category_id",
                table: "products");

            migrationBuilder.DropColumn(
                name: "barcode",
                table: "products");

            migrationBuilder.DropColumn(
                name: "category_id",
                table: "products");

            migrationBuilder.DropColumn(
                name: "description",
                table: "products");

            migrationBuilder.DropColumn(
                name: "height_cm",
                table: "products");

            migrationBuilder.DropColumn(
                name: "length_cm",
                table: "products");

            migrationBuilder.DropColumn(
                name: "weight_kg",
                table: "products");

            migrationBuilder.DropColumn(
                name: "width_cm",
                table: "products");
        }
    }
}