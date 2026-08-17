import { createFileRoute } from "@tanstack/react-router";
import { SecurityTab } from "../settings";
export const Route = createFileRoute("/settings/security")({ component: SecurityTab });
