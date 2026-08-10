import { createFileRoute } from "@tanstack/react-router";
import { CategoriesPage } from "@vantigo/products-ui/pages/categories";
export const Route = createFileRoute("/products/categories")({ component: CategoriesPage });
