import { createFileRoute } from "@tanstack/react-router";
import { TrashRoutePage } from "./-knowledge-pages";

/** The trash (knowledge design K10). */
export const Route = createFileRoute("/knowledge/trash")({
  component: TrashRoutePage,
});
