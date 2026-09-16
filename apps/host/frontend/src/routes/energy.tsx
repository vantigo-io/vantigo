import { createFileRoute } from "@tanstack/react-router";
import { AppLayout } from "./-app-layout";

export const Route = createFileRoute("/energy")({
  staticData: { app: "energy" },
  component: () => <AppLayout app="energy" />,
});
