import { createFileRoute } from "@tanstack/react-router";
import { ComposePage } from "@vantigo/communications-ui/pages/messages.compose";
export const Route = createFileRoute("/messages/compose")({ component: ComposePage });
