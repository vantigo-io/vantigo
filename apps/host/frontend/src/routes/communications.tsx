import { createFileRoute } from "@tanstack/react-router";
import { AppLayout } from "./-app-layout";

export const Route = createFileRoute("/communications")({
  staticData: { app: "communications" },
  component: () => <AppLayout app="communications" />,
});
