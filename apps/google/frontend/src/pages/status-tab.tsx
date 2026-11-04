import { Alert, Group, Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import { PageLink } from "../components/page-link";
import { EventsCard, StatusCard } from "../components/status-card";
import "../i18n";
import { googleTabPath } from "../lib/tabs";
import { TabFrame } from "./tab-frame";

/**
 * The **Status** tab (Google Workspace design G16): the status and its
 * reason, the organisation as the last test read it, the scopes granted,
 * missing and unexpected, and the module's recent events. While nothing is
 * set up it says so and points to the Connection tab.
 */
export function GoogleStatusTab() {
  const { t } = useI18n("google");
  return (
    <TabFrame>
      {({ connection }) => (
        <>
          {connection.status === "unconfigured" && (
            <Alert color="blue" variant="light" title={t("notConnectedYet")}>
              <Group gap={4}>
                <Text size="sm">{t("notConnectedYetBody")}</Text>
                <PageLink to={googleTabPath("connection")}>{t("tabConnection")}</PageLink>
              </Group>
            </Alert>
          )}
          <StatusCard connection={connection} />
          <EventsCard />
        </>
      )}
    </TabFrame>
  );
}
