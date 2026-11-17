import { createFileRoute } from "@tanstack/react-router";
import { SettingsRoutePage } from "./-purchasing-pages";

/** Purchasing's settings (P9); the page saves with purchasing:settings. */
export const Route = createFileRoute("/purchasing/settings")({
  component: SettingsRoutePage,
});
