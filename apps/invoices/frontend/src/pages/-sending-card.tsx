import { Badge, Group, Radio, Stack, Text, Title } from "@mantine/core";
import { PageSection } from "@vantigo/frontend-shell";
import type { InvoiceSettings } from "../api/settings";
import "../i18n";
import { useInvoiceFormat } from "../lib/format";

/** The radio value of Disabled: no channel, which the server stores as null. */
const DISABLED = "";

interface SendingCardProps {
  /** The settings as saved: the channels a choice may name, and what the stored one is now. */
  settings: InvoiceSettings;
  /** The form's values, the choice being edited among them. */
  values: InvoiceSettings;
  errors: Record<string, string>;
  set: <K extends keyof InvoiceSettings>(key: K, value: InvoiceSettings[K]) => void;
}

/**
 * Sending (integrations design T6): the channel documents and reminder letters
 * are sent from, chosen among the mail channels Communications offers, or
 * Disabled. A channel shows its provider as a badge; one that cannot send
 * says so and is not offered unless it is the stored choice. A stored channel
 * that no longer exists reads as "Disabled — the channel no longer exists" and
 * is kept until an Owner chooses again. The choice is saved with the rest of
 * the settings.
 */
export const SendingCard = ({ settings, values, set, errors }: SendingCardProps) => {
  const { t } = useInvoiceFormat();
  const channels = settings.sendChannels ?? [];
  const chosen = values.sendChannel ?? null;
  // A choice no listed channel has: the stored one, gone.
  const gone = chosen !== null && !channels.some((channel) => channel.id === chosen);
  const selected = gone || chosen === null ? DISABLED : chosen;
  return (
    <PageSection data-testid="sending-card">
      <Stack>
        <Title order={4}>{t("sending")}</Title>
        <Radio.Group
          label={t("sendingLabel")}
          description={t("sendingDescription")}
          value={selected}
          error={errors.sendChannel}
          onChange={(value) => set("sendChannel", value === DISABLED ? null : value)}
        >
          <Stack gap="xs" mt="xs">
            {channels.map((channel) => (
              <Radio
                key={channel.id}
                value={channel.id}
                disabled={!channel.available && channel.id !== chosen}
                label={
                  <Group gap="xs">
                    <Text span fw={600}>
                      {channel.displayName || channel.address}
                    </Text>
                    {channel.displayName && (
                      <Text span size="sm" c="dimmed">
                        {channel.address}
                      </Text>
                    )}
                    <Badge variant="light">{channel.providerDisplayName}</Badge>
                    {!channel.available && (
                      <Badge color="orange" variant="light">
                        {t("sendingCannotSend")}
                      </Badge>
                    )}
                  </Group>
                }
              />
            ))}
            <Radio value={DISABLED} label={gone ? t("sendingGone") : t("sendingDisabled")} />
          </Stack>
        </Radio.Group>
        {channels.length === 0 && (
          <Text size="sm" c="dimmed">
            {t("sendingNoChannels")}
          </Text>
        )}
      </Stack>
    </PageSection>
  );
};
