import { Stack } from "@mantine/core";
import { createFileRoute, notFound, useRouter } from "@tanstack/react-router";
import { OutlookConnectionCard } from "@vantigo/entra-ui";
import { PageHeader, useTranslation } from "@vantigo/frontend-shell";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import "../../i18n";

export interface ConnectionsSearch {
  outlook?: "complete" | "failed";
  code?: string;
}

/**
 * What Entra's callback redirects here with (Outlook connection design M4):
 * `outlook=complete`, or `outlook=failed` with a `code`. Anything else is
 * dropped, and a code is kept only as a short lower-case word: the card
 * looks its sentence up and never shows it as it came.
 */
export const validateConnectionsSearch = (search: Record<string, unknown>): ConnectionsSearch => {
  const outlook = search.outlook === "complete" || search.outlook === "failed" ? search.outlook : undefined;
  const code =
    outlook === "failed" && typeof search.code === "string" && /^[a-z_]{1,40}$/.test(search.code)
      ? search.code
      : undefined;
  return { ...(outlook ? { outlook } : {}), ...(code ? { code } : {}) };
};

/**
 * Settings › Connections (Outlook connection design M8): the person's own
 * connections to accounts elsewhere, composed from the Entra package — today
 * their Outlook. Shown while the Entra module is on. A completed connect
 * returns to where it began, replacing the callback's address in the history
 * so Back never completes it twice.
 */
const ConnectionsPage = () => {
  const { t } = useTranslation("host");
  const router = useRouter();
  const { outlook, code } = Route.useSearch();
  return (
    <Stack gap="xl">
      <PageHeader title={t("settings.connections")} description={t("settings.connectionsDescription")} />
      <OutlookConnectionCard
        outcome={outlook ? { outlook, ...(code ? { code } : {}) } : undefined}
        onReturn={(to) => router.history.replace(to)}
      />
    </Stack>
  );
};

export const Route = createFileRoute("/settings/connections")({
  validateSearch: validateConnectionsSearch,
  beforeLoad: () => {
    if (!enabledModuleKeys().includes("entra")) throw notFound();
  },
  component: ConnectionsPage,
});
