import { Group, Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import { OutlookSettingsForm } from "../components/outlook-settings-form";
import { PageLink } from "../components/page-link";
import { SectionCard } from "../components/step-card";
import "../i18n";
import { TabFrame } from "./tab-frame";

/**
 * The **Outlook and mail** tab: the Outlook connection switch (Outlook
 * connection design M2) — which calendar sync runs on, with its change
 * notifications — and the Mail channels switch (M10), each with where it
 * stands, saved together under `entra:manage`. Once Mail channels is on, a
 * link leads to Communications' **Channels**, where the mailboxes are added:
 * channels stay a Communications object (Integrations design T2).
 */
export function EntraOutlookTab() {
  const { t } = useI18n("entra");
  return (
    <TabFrame>
      {({ connection }) => (
        <SectionCard title={t("outlookAndMail")}>
          <Text size="sm" c="dimmed">
            {t("outlookAndMailDescription")}
          </Text>
          <OutlookSettingsForm connection={connection} />
          {connection.mailChannels.enabled && (
            <Group gap={6}>
              <Text size="sm">{t("mailChannelsAddMailbox")}</Text>
              <PageLink to="/communications/channels">{t("mailChannelsOpenChannels")}</PageLink>
            </Group>
          )}
        </SectionCard>
      )}
    </TabFrame>
  );
}
