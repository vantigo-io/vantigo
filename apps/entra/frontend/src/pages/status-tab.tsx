import { Alert, Badge, Group, Stack, Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import { PageLink } from "../components/page-link";
import { StatusCard } from "../components/status-card";
import { SectionCard } from "../components/step-card";
import "../i18n";
import { nextStepTab, requiredStepsDone } from "../lib/steps";
import { type EntraTab, entraTabLabelKey, entraTabPath } from "../lib/tabs";
import { TabFrame } from "./tab-frame";

/** How many steps carry a mark: steps 1 to 4. The other two are optional. */
const REQUIRED_STEPS = 4;

/**
 * The **Status** tab (Entra design D15, Integrations design T3): how far the
 * set-up is and where to continue, the **Status** card once the connection
 * has been tested, and one line per switch — the Outlook connection, which
 * calendar sync runs on, and the Mail channels — saying whether it is on and
 * linking to the tab where it is turned on or off.
 */
export function EntraStatusTab() {
  const { t } = useI18n("entra");
  return (
    <TabFrame>
      {({ connection, owner }) => {
        const next = nextStepTab(connection);
        return (
          <>
            {next && (
              <Alert color="blue" variant="light" role="status" aria-label={t("stepsProgressTitle")}>
                <Group gap={4}>
                  <Text size="sm">
                    {t("stepsProgress", { done: requiredStepsDone(connection), total: REQUIRED_STEPS })}
                  </Text>
                  <TabLink tab={next} />
                </Group>
              </Alert>
            )}
            {connection.status === "unconfigured" ? (
              <Alert color="gray" variant="light" title={t("notConnectedYet")}>
                <Text size="sm">{t("notConnectedYetBody")}</Text>
              </Alert>
            ) : (
              <StatusCard connection={connection} owner={owner} />
            )}
            <SectionCard title={t("switchesTitle")}>
              <Stack
                gap="xs"
                component="ul"
                aria-label={t("switchesTitle")}
                style={{ listStyle: "none", padding: 0, margin: 0 }}
              >
                <SwitchLine
                  label={t("switchOutlook")}
                  description={t("switchOutlookDescription")}
                  on={connection.outlookConnect.enabled}
                />
                <SwitchLine
                  label={t("switchMail")}
                  description={t("switchMailDescription")}
                  on={connection.mailChannels.enabled}
                />
              </Stack>
            </SectionCard>
          </>
        );
      }}
    </TabFrame>
  );
}

const TabLink = ({ tab }: { tab: EntraTab }) => {
  const { t } = useI18n("entra");
  return <PageLink to={entraTabPath(tab)}>{t(entraTabLabelKey(tab))}</PageLink>;
};

const SwitchLine = ({ label, description, on }: { label: string; description: string; on: boolean }) => {
  const { t } = useI18n("entra");
  return (
    <Group component="li" gap="sm" wrap="nowrap" align="flex-start" justify="space-between">
      <Stack gap={0}>
        <Text size="sm" fw={500}>
          {label}
        </Text>
        <Text size="xs" c="dimmed">
          {description}
        </Text>
      </Stack>
      <Group gap="sm" wrap="nowrap">
        <Badge color={on ? "teal" : "gray"} variant={on ? "light" : "outline"}>
          {on ? t("switchOn") : t("switchOff")}
        </Badge>
        <TabLink tab="outlook" />
      </Group>
    </Group>
  );
};
