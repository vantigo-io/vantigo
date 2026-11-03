import { Stack } from "@mantine/core";
import { createFileRoute, notFound, useRouter } from "@tanstack/react-router";
import { PageHeader, useTranslation } from "@vantigo/frontend-shell";
import { ConnectionCards, enabledConnectionProviders, validateConnectionsSearch } from "../../lib/connections";
import "../../i18n";

/**
 * Settings › Connections (Outlook connection design M8, Google Workspace
 * design G9): the person's own connections to accounts elsewhere, one card per
 * enabled provider in the host's list (lib/connections.tsx) — today Entra's
 * Outlook. A completed connect returns to where it began, replacing the
 * callback's address in the history so Back never completes it twice.
 */
const ConnectionsPage = () => {
  const { t } = useTranslation("host");
  const router = useRouter();
  const search = Route.useSearch();
  return (
    <Stack gap="xl">
      <PageHeader title={t("settings.connections")} description={t("settings.connectionsDescription")} />
      <ConnectionCards
        providers={enabledConnectionProviders()}
        search={search}
        onReturn={(to) => router.history.replace(to)}
      />
    </Stack>
  );
};

export const Route = createFileRoute("/settings/connections")({
  validateSearch: validateConnectionsSearch,
  beforeLoad: () => {
    if (enabledConnectionProviders().length === 0) throw notFound();
  },
  component: ConnectionsPage,
});
