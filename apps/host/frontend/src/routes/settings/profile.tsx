import { createFileRoute } from "@tanstack/react-router";
import { ProfileTab } from "../settings";
export const Route = createFileRoute("/settings/profile")({ component: ProfileTab });
