import { createFileRoute } from "@tanstack/react-router";
import { TrashPage } from "@vantigo/knowledge-ui/pages/trash";

/** The trash (knowledge design K10). */
export const Route = createFileRoute("/knowledge/trash")({
  component: TrashPage,
});
