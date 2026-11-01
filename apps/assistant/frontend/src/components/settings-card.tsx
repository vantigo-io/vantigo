import {
  Alert,
  Button,
  Card,
  Group,
  Loader,
  NumberInput,
  Radio,
  SegmentedControl,
  Stack,
  Text,
  Title,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconInfoCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  isVersionConflict,
  permissionsQueryKey,
  putSettings,
  settingsQueryKey,
  settingsQueryOptions,
} from "../api/assistant";
import type { AssistantMode, AssistantPermissions, AssistantSettings, AssistantSettingsRequest } from "../api/types";
import { PermissionsTable, type PermissionsTranslation } from "./permissions-table";
import "../i18n";

const modes: readonly { value: AssistantMode; label: string; hint: string }[] = [
  { value: "all", label: "modeAll", hint: "modeAllHint" },
  { value: "per_person", label: "modePerPerson", hint: "modePerPersonHint" },
  { value: "disabled", label: "modeDisabled", hint: "modeDisabledHint" },
];

/**
 * Where the data goes (S7), worded for the Owner who turns the assistant or
 * the sensitive data on: a provider that is not local gets "Questions and the
 * data the assistant reads are sent to <provider>".
 */
export const DataNotice = ({ settings }: { settings: Pick<AssistantSettings, "local" | "provider"> }) => {
  const { t } = useI18n("assistant");
  if (!settings.local)
    return (
      <Alert color="yellow" icon={<IconInfoCircle size={16} />} data-testid="data-notice">
        {t("dataNoticeRemote", { provider: settings.provider || t("assistant") })}
      </Alert>
    );
  return (
    <Text size="sm" c="dimmed" data-testid="data-notice">
      {t("dataNoticeLocal")}
    </Text>
  );
};

export interface AssistantSettingsCardProps {
  /** The host's words for the catalog's modules, categories and permissions. */
  translate?: PermissionsTranslation;
}

/**
 * The Owner's Assistant page (Workspace admin › Assistant): who has the
 * assistant, how long conversations are kept, the data notice, and *What the
 * assistant may use* (S16). Every save names the settings' version in
 * If-Match; a stale one reloads and says so.
 */
export const AssistantSettingsCard = ({ translate }: AssistantSettingsCardProps) => {
  const { t } = useI18n("assistant");
  const queryClient = useQueryClient();
  const query = useQuery(settingsQueryOptions());
  const settings = query.data;
  // What the Owner has typed, until it is saved; the saved value shows otherwise.
  const [draft, setDraft] = useState<number | string | undefined>();
  const retention = draft ?? settings?.retentionMonths ?? 12;

  const save = useMutation({
    mutationFn: (patch: AssistantSettingsRequest) => putSettings(patch, settings?.version ?? 0),
    onSuccess: (saved, patch) => {
      queryClient.setQueryData(settingsQueryKey, saved);
      setDraft(undefined);
      // The permissions share the settings' version, and the switch moves their defaults.
      queryClient.setQueryData<AssistantPermissions>(
        permissionsQueryKey,
        (current) => current && { ...current, version: saved.version },
      );
      if (patch.sensitiveDefault !== undefined) void queryClient.invalidateQueries({ queryKey: permissionsQueryKey });
      notifications.show({ color: "teal", title: t("saved"), message: "" });
    },
    onError: (error) => {
      if (isVersionConflict(error)) {
        notifications.show({ color: "yellow", title: t("staleTitle"), message: t("staleBody") });
        void queryClient.invalidateQueries({ queryKey: settingsQueryKey });
        void queryClient.invalidateQueries({ queryKey: permissionsQueryKey });
        return;
      }
      notifications.show({
        color: "red",
        title: t("saveFailed"),
        message: error instanceof Error && error.message ? error.message : t("requestFailed"),
      });
    },
  });

  if (query.isPending)
    return (
      <Card withBorder>
        <Loader size="sm" />
      </Card>
    );
  if (query.isError || !settings)
    return (
      <Card withBorder>
        <Group>
          <Text size="sm" c="red">
            {t("requestFailed")}
          </Text>
          <Button size="xs" variant="light" onClick={() => void query.refetch()}>
            {t("tryAgain")}
          </Button>
        </Group>
      </Card>
    );

  const retentionNumber = typeof retention === "number" ? retention : Number.NaN;
  const retentionValid = Number.isInteger(retentionNumber) && retentionNumber >= 1 && retentionNumber <= 60;

  return (
    <Stack gap="md">
      <Card withBorder>
        <Stack gap="md">
          <Stack gap={4}>
            <Title order={4}>{t("settingsTitle")}</Title>
            <Text size="sm" c="dimmed">
              {t("settingsIntro")}
            </Text>
          </Stack>
          {!settings.configured && <Alert color="yellow">{t("notConfigured")}</Alert>}
          <DataNotice settings={settings} />
          <Radio.Group
            label={t("modeLabel")}
            value={settings.mode}
            onChange={(value) => save.mutate({ mode: value as AssistantMode })}
          >
            <Stack gap="xs" mt="xs">
              {modes.map((mode) => (
                <Radio
                  key={mode.value}
                  value={mode.value}
                  label={t(mode.label)}
                  description={t(mode.hint)}
                  disabled={save.isPending}
                />
              ))}
            </Stack>
          </Radio.Group>
          <Group align="flex-end">
            <NumberInput
              label={t("retentionLabel")}
              description={t("retentionDescription")}
              min={1}
              max={60}
              allowDecimal={false}
              suffix={` ${t("retentionUnit")}`}
              value={retention}
              onChange={setDraft}
              w={260}
            />
            <Button
              disabled={!retentionValid || retentionNumber === settings.retentionMonths}
              loading={save.isPending}
              onClick={() => save.mutate({ retentionMonths: retentionNumber })}
            >
              {t("save")}
            </Button>
          </Group>
        </Stack>
      </Card>

      <Card withBorder>
        <Stack gap="md">
          <Stack gap={4}>
            <Title order={4}>{t("mayUseTitle")}</Title>
            <Text size="sm" c="dimmed">
              {t("mayUseIntro")}
            </Text>
          </Stack>
          <Stack gap="xs">
            <Text size="sm" fw={500}>
              {t("sensitiveTitle")}
            </Text>
            <Group align="flex-start" gap="md">
              <SegmentedControl
                aria-label={t("sensitiveTitle")}
                value={settings.sensitiveDefault}
                disabled={save.isPending}
                onChange={(value) => save.mutate({ sensitiveDefault: value as AssistantSettings["sensitiveDefault"] })}
                data={[
                  { value: "off", label: t("sensitiveOff") },
                  { value: "read", label: t("sensitiveRead") },
                ]}
              />
              <Stack gap={4} maw={420} flex={1}>
                <Text size="xs" c="dimmed">
                  {t("sensitiveHint")}
                </Text>
                <DataNotice settings={settings} />
              </Stack>
            </Group>
          </Stack>
          <PermissionsTable translate={translate} />
        </Stack>
      </Card>
    </Stack>
  );
};
