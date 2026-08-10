import { createFileRoute } from "@tanstack/react-router";
import { MessageDetailsPage } from "@vantigo/communications-ui";
export const Route = createFileRoute("/messages/$messageId")({ component: MessageDetailsPage });
