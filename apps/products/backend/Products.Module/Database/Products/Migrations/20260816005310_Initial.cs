using System;

using Microsoft.EntityFrameworkCore.Migrations;

using Npgsql.EntityFrameworkCore.PostgreSQL.Metadata;

using Vantigo.Tenancy.EntityFramework;

#nullable disable

namespace Vantigo.Products.Database.Products.Migrations
{
    /// <inheritdoc />
    public partial class Initial : Migration
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
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false, comment: "The tenant that owns the category"),
                    name = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false, comment: "The display name of the category, unique among its siblings"),
                    parent_id = table.Column<int>(type: "integer", nullable: true, comment: "The parent category; null means the category is a root")
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_product_categories", x => new { x.tenant_id, x.id });
                    table.ForeignKey(
                        name: "FK_product_categories_product_categories_tenant_id_parent_id",
                        columns: x => new { x.tenant_id, x.parent_id },
                        principalSchema: "products",
                        principalTable: "product_categories",
                        principalColumns: new[] { "tenant_id", "id" },
                        onDelete: ReferentialAction.Restrict);
                });

            migrationBuilder.CreateTable(
                name: "tax_categories",
                schema: "products",
                columns: table => new
                {
                    id = table.Column<int>(type: "integer", nullable: false, comment: "The unique identifier of the tax category")
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false, comment: "The tenant that owns the tax category"),
                    name = table.Column<string>(type: "character varying(100)", maxLength: 100, nullable: false, comment: "The unique display name of the tax category"),
                    kind = table.Column<string>(type: "character varying(20)", unicode: false, maxLength: 20, nullable: false, comment: "The tax treatment kind"),
                    rate = table.Column<decimal>(type: "numeric(5,4)", precision: 5, scale: 4, nullable: false, comment: "The tax rate as a fraction, for example 0.25 for 25%"),
                    created_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false, comment: "When the tax category was created"),
                    updated_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false, comment: "When the tax category was last updated")
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_tax_categories", x => new { x.tenant_id, x.id });
                });

            migrationBuilder.CreateTable(
                name: "products",
                schema: "products",
                columns: table => new
                {
                    id = table.Column<int>(type: "integer", nullable: false, comment: "The unique identifier of the product")
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false, comment: "The tenant that owns the product"),
                    name = table.Column<string>(type: "character varying(200)", maxLength: 200, nullable: false, comment: "The display name of the product"),
                    description = table.Column<string>(type: "character varying(4000)", maxLength: 4000, nullable: true, comment: "A free-form plain-text description of the product"),
                    category_id = table.Column<int>(type: "integer", nullable: true, comment: "The category the product belongs to; null means uncategorised"),
                    type = table.Column<string>(type: "character varying(20)", unicode: false, maxLength: 20, nullable: false, comment: "Whether the product is a physical good or a performed service"),
                    status = table.Column<string>(type: "character varying(20)", unicode: false, maxLength: 20, nullable: false, comment: "The lifecycle status of the product"),
                    tax_category_id = table.Column<int>(type: "integer", nullable: false, comment: "The tax category applied when the product is sold"),
                    created_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false, comment: "When the product was created"),
                    updated_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false, comment: "When the product was last updated")
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_products", x => new { x.tenant_id, x.id });
                    table.ForeignKey(
                        name: "FK_products_product_categories_tenant_id_category_id",
                        columns: x => new { x.tenant_id, x.category_id },
                        principalSchema: "products",
                        principalTable: "product_categories",
                        principalColumns: new[] { "tenant_id", "id" },
                        onDelete: ReferentialAction.Restrict);
                    table.ForeignKey(
                        name: "FK_products_tax_categories_tenant_id_tax_category_id",
                        columns: x => new { x.tenant_id, x.tax_category_id },
                        principalSchema: "products",
                        principalTable: "tax_categories",
                        principalColumns: new[] { "tenant_id", "id" },
                        onDelete: ReferentialAction.Restrict);
                });

            migrationBuilder.CreateTable(
                name: "product_variants",
                schema: "products",
                columns: table => new
                {
                    id = table.Column<int>(type: "integer", nullable: false, comment: "The unique identifier of the product variant")
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false, comment: "The tenant that owns the variant"),
                    product_id = table.Column<int>(type: "integer", nullable: false, comment: "The product that owns the variant"),
                    sku = table.Column<string>(type: "character varying(64)", unicode: false, maxLength: 64, nullable: false, comment: "The stock keeping unit uniquely identifying this sellable variant"),
                    barcode = table.Column<string>(type: "character varying(14)", unicode: false, maxLength: 14, nullable: true, comment: "The GTIN barcode of the variant, unique when set"),
                    unit = table.Column<string>(type: "character varying(20)", unicode: false, maxLength: 20, nullable: false, comment: "The unit the variant is sold in, for instance pcs or hour"),
                    standard_cost = table.Column<decimal>(type: "numeric(12,2)", precision: 12, scale: 2, nullable: true, comment: "An indicative cost in the company base currency excluding VAT"),
                    weight_kg = table.Column<decimal>(type: "numeric(10,3)", precision: 10, scale: 3, nullable: true, comment: "The gross weight of one unit in kilograms"),
                    length_cm = table.Column<decimal>(type: "numeric(10,1)", precision: 10, scale: 1, nullable: true, comment: "The length of one unit in centimetres"),
                    width_cm = table.Column<decimal>(type: "numeric(10,1)", precision: 10, scale: 1, nullable: true, comment: "The width of one unit in centimetres"),
                    height_cm = table.Column<decimal>(type: "numeric(10,1)", precision: 10, scale: 1, nullable: true, comment: "The height of one unit in centimetres"),
                    option_values = table.Column<string>(type: "jsonb", nullable: false, comment: "The option values distinguishing this variant"),
                    created_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false, comment: "When the variant was created"),
                    updated_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false, comment: "When the variant was last updated")
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_product_variants", x => new { x.tenant_id, x.id });
                    table.ForeignKey(
                        name: "FK_product_variants_products_tenant_id_product_id",
                        columns: x => new { x.tenant_id, x.product_id },
                        principalSchema: "products",
                        principalTable: "products",
                        principalColumns: new[] { "tenant_id", "id" },
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "product_prices",
                schema: "products",
                columns: table => new
                {
                    id = table.Column<int>(type: "integer", nullable: false, comment: "The unique identifier of the price")
                        .Annotation("Npgsql:IdentitySequenceOptions", "'1001', '1', '', '', 'False', '1'")
                        .Annotation("Npgsql:ValueGenerationStrategy", NpgsqlValueGenerationStrategy.IdentityByDefaultColumn),
                    tenant_id = table.Column<Guid>(type: "uuid", nullable: false, comment: "The tenant that owns the price row"),
                    variant_id = table.Column<int>(type: "integer", nullable: false, comment: "The product variant the price belongs to"),
                    currency = table.Column<string>(type: "character(3)", unicode: false, fixedLength: true, maxLength: 3, nullable: false, comment: "The ISO 4217 currency code of the price"),
                    amount = table.Column<decimal>(type: "numeric(12,2)", precision: 12, scale: 2, nullable: false, comment: "The price amount in the given currency, excluding VAT"),
                    valid_from = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true, comment: "When the price becomes valid; null means valid from the beginning of time"),
                    valid_to = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: true, comment: "When the price stops being valid (exclusive); null means open-ended")
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_product_prices", x => new { x.tenant_id, x.id });
                    table.ForeignKey(
                        name: "FK_product_prices_product_variants_tenant_id_variant_id",
                        columns: x => new { x.tenant_id, x.variant_id },
                        principalSchema: "products",
                        principalTable: "product_variants",
                        principalColumns: new[] { "tenant_id", "id" },
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateIndex(
                name: "IX_product_categories_tenant_id_parent_id",
                schema: "products",
                table: "product_categories",
                columns: new[] { "tenant_id", "parent_id" });

            migrationBuilder.CreateIndex(
                name: "IX_product_categories_tenant_id_parent_id_name",
                schema: "products",
                table: "product_categories",
                columns: new[] { "tenant_id", "parent_id", "name" },
                unique: true)
                .Annotation("Npgsql:NullsDistinct", false);

            migrationBuilder.CreateIndex(
                name: "IX_product_prices_tenant_id_variant_id",
                schema: "products",
                table: "product_prices",
                columns: new[] { "tenant_id", "variant_id" });

            migrationBuilder.CreateIndex(
                name: "IX_product_prices_tenant_id_variant_id_currency",
                schema: "products",
                table: "product_prices",
                columns: new[] { "tenant_id", "variant_id", "currency" });

            migrationBuilder.CreateIndex(
                name: "IX_product_variants_tenant_id_barcode",
                schema: "products",
                table: "product_variants",
                columns: new[] { "tenant_id", "barcode" },
                unique: true,
                filter: "barcode IS NOT NULL");

            migrationBuilder.CreateIndex(
                name: "IX_product_variants_tenant_id_product_id",
                schema: "products",
                table: "product_variants",
                columns: new[] { "tenant_id", "product_id" });

            migrationBuilder.CreateIndex(
                name: "IX_product_variants_tenant_id_sku",
                schema: "products",
                table: "product_variants",
                columns: new[] { "tenant_id", "sku" },
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_products_tenant_id_category_id",
                schema: "products",
                table: "products",
                columns: new[] { "tenant_id", "category_id" });

            migrationBuilder.CreateIndex(
                name: "IX_products_tenant_id_tax_category_id",
                schema: "products",
                table: "products",
                columns: new[] { "tenant_id", "tax_category_id" });

            migrationBuilder.CreateIndex(
                name: "IX_tax_categories_tenant_id_name",
                schema: "products",
                table: "tax_categories",
                columns: new[] { "tenant_id", "name" },
                unique: true);

            migrationBuilder.EnableTenantRls("products", "product_categories");
            migrationBuilder.EnableTenantRls("products", "tax_categories");
            migrationBuilder.EnableTenantRls("products", "products");
            migrationBuilder.EnableTenantRls("products", "product_variants");
            migrationBuilder.EnableTenantRls("products", "product_prices");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DisableTenantRls("products", "product_prices");
            migrationBuilder.DisableTenantRls("products", "product_variants");
            migrationBuilder.DisableTenantRls("products", "products");
            migrationBuilder.DisableTenantRls("products", "tax_categories");
            migrationBuilder.DisableTenantRls("products", "product_categories");

            migrationBuilder.DropTable(
                name: "product_prices",
                schema: "products");

            migrationBuilder.DropTable(
                name: "product_variants",
                schema: "products");

            migrationBuilder.DropTable(
                name: "products",
                schema: "products");

            migrationBuilder.DropTable(
                name: "product_categories",
                schema: "products");

            migrationBuilder.DropTable(
                name: "tax_categories",
                schema: "products");
        }
    }
}