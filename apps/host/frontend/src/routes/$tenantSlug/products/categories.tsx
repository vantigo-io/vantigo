import { createFileRoute } from "@tanstack/react-router";
import { CategoriesPage } from "@vantigo/products-ui/pages/categories";
export const Route = createFileRoute("/$tenantSlug/products/categories")({ component: CategoriesPage });
