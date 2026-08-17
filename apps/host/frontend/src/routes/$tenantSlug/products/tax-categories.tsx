import { createFileRoute } from "@tanstack/react-router";
import { TaxCategoriesPage } from "@vantigo/products-ui/pages/tax-categories";
export const Route = createFileRoute("/$tenantSlug/products/tax-categories")({ component: TaxCategoriesPage });
