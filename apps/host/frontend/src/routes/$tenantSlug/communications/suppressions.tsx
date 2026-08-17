import { createFileRoute } from "@tanstack/react-router";
import { SuppressionsPage } from "@vantigo/communications-ui/pages/admin.suppressions";
export const Route = createFileRoute("/$tenantSlug/communications/suppressions")({ component: SuppressionsPage });
