import { createFileRoute } from "@tanstack/react-router";
import { AppLayout } from "./-app-layout";

export const Route = createFileRoute("/products")({
  staticData: { app: "products" },
  component: () => <AppLayout app="products" />,
});
