import { createFileRoute } from "@tanstack/react-router";
import { AppLayout } from "./-app-layout";

export const Route = createFileRoute("/customers")({
  staticData: { app: "customers" },
  component: () => <AppLayout app="customers" />,
});
