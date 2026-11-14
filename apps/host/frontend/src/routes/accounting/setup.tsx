import { createFileRoute } from "@tanstack/react-router";
import { SetupPage } from "@vantigo/accounting-ui/pages/setup";

/** Set up the books, once (accounting design R12); the guard asks for accounting:read, the page for accounting:close. */
export const Route = createFileRoute("/accounting/setup")({
  component: SetupPage,
});
