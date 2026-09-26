import { createFileRoute } from "@tanstack/react-router";
import { appLayoutOptions } from "./-app-layout";

export const Route = createFileRoute("/invoices")(appLayoutOptions("invoices"));
