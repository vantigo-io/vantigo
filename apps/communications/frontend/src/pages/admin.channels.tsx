import { Alert, Badge, Button, Card, Group, Modal, Select, Stack, Table, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type Channel,
  type ChannelImportWindow,
  type ChannelOnClose,
  type ChannelSync,
  channelProvidersQueryOptions,
  channelsQueryOptions,
  type SwitchChannelProviderOptions,
  switchChannelProvider,
  updateChannel,
  verifyChannel,
} from "../api/channels";
import { ChannelForm } from "../components/ChannelForm";
import { channelErrorMessage } from "../components/channelErrors";
import { relative } from "../components/conversationHelpers";
import "../i18n";

const syncErrors = ["mailbox_denied", "mailbox_gone", "mailbox_unknown", "provider_unavailable", "sync_failed"];

/** Whether a channel receives: a Microsoft 365 channel's mailbox sync, by its state and last failure; SMTP sends only. */
function ReceiveStatus({ sync }: { sync: ChannelSync | null | undefined }) {
  const { t } = useI18n("communications");
  if (!sync)
    return (
      <Text size="sm" c="dimmed">
        {t("sendsOnly")}
      </Text>
    );
  const reason = sync.error
    ? t(`syncError.${syncErrors.includes(sync.error) ? sync.error : "sync_failed"}`)
    : t("syncError.sync_failed");
  const label =
    sync.state === "failed"
      ? t("sync.failed", { reason })
      : sync.error
        ? t("sync.retrying", { reason })
        : sync.state === "pending"
          ? t("sync.pending")
          : sync.state === "stopped"
            ? t("sync.stopped")
            : t("sync.active");
  const color = sync.state === "failed" ? "red" : sync.error ? "orange" : sync.state === "active" ? "teal" : "gray";
  return (
    <Stack gap={2}>
      <Text size="sm" c={color === "gray" ? "dimmed" : color}>
        {label}
      </Text>
      {sync.lastSyncedAt && (
        <Text size="xs" c="dimmed">
          {t("lastSynced", { time: relative(sync.lastSyncedAt) })}
        </Text>
      )}
    </Stack>
  );
}

export function ChannelsPage() {
  const { t, locale } = useI18n("communications");
  const query = useQuery(channelsQueryOptions());
  const providersQuery = useQuery(channelProvidersQueryOptions());
  const providers = providersQuery.data ?? [];
  const entra = providers.find((provider) => provider.key === "entra");
  const client = useQueryClient();
  const [opened, setOpened] = useState(false);
  const [switching, setSwitching] = useState<Channel | null>(null);
  const [switchImportWindow, setSwitchImportWindow] = useState<ChannelImportWindow>("from_now");
  const [switchOnClose, setSwitchOnClose] = useState<ChannelOnClose>("none");
  const [switchFolder, setSwitchFolder] = useState("Handled in Vantigo");
  const [editingOnClose, setEditingOnClose] = useState<Channel | null>(null);
  const [editOnCloseValue, setEditOnCloseValue] = useState<ChannelOnClose>("none");
  const [editFolderValue, setEditFolderValue] = useState("Handled in Vantigo");
  const refresh = () => client.invalidateQueries({ queryKey: ["channels"] });
  // The badge and the select name a provider as the provider names itself;
  // a channel whose provider is not offered now still reads as a name.
  const providerLabel = (key: string) =>
    providers.find((provider) => provider.key === key)?.displayName ??
    (key === "smtp" ? t("smtp") : key === "entra" ? t("microsoft365") : key);
  const update = useMutation({
    mutationFn: ({ id, isActive }: { id: string; isActive: boolean }) => updateChannel(id, { isActive }),
    onSuccess: () => void refresh(),
  });
  const saveOnClose = useMutation({
    mutationFn: ({ id, onClose, folder }: { id: string; onClose: ChannelOnClose; folder: string }) =>
      updateChannel(id, {
        onClose,
        ...(onClose === "move" ? { folder: folder.trim() || "Handled in Vantigo" } : {}),
      }),
    onSuccess: () => {
      setEditingOnClose(null);
      void refresh();
      notifications.show({ title: t("channelUpdated"), message: t("emailChannelReady") });
    },
  });
  const verify = useMutation({
    mutationFn: (channel: Channel) => verifyChannel(channel.id),
    onSuccess: () => notifications.show({ title: t("channelVerified"), message: t("channelConnectionValid") }),
    onError: (error) =>
      notifications.show({
        color: "red",
        title: t("channelNotVerified"),
        message: channelErrorMessage(error, t, locale, "errorVerificationFailed"),
      }),
  });
  const switchProvider = useMutation({
    mutationFn: (channel: Channel) => {
      const opts: SwitchChannelProviderOptions = {
        ...(switchImportWindow !== "from_now" ? { importWindow: switchImportWindow } : {}),
        ...(switchOnClose !== "none" ? { onClose: switchOnClose } : {}),
        ...(switchOnClose === "move" && switchFolder.trim() ? { folder: switchFolder.trim() } : {}),
      };
      return Object.keys(opts).length > 0 ? switchChannelProvider(channel.id, opts) : switchChannelProvider(channel.id);
    },
    onSuccess: () => {
      setSwitching(null);
      setSwitchImportWindow("from_now");
      setSwitchOnClose("none");
      setSwitchFolder("Handled in Vantigo");
      void refresh();
      notifications.show({ title: t("channelSwitched"), message: t("channelSwitchedMessage") });
    },
  });
  const closeSwitch = () => {
    setSwitching(null);
    setSwitchImportWindow("from_now");
    setSwitchOnClose("none");
    setSwitchFolder("Handled in Vantigo");
    switchProvider.reset();
  };
  const openEditOnClose = (channel: Channel) => {
    setEditingOnClose(channel);
    setEditOnCloseValue(channel.mailbox?.onClose ?? "none");
    setEditFolderValue(channel.mailbox?.folder || "Handled in Vantigo");
    saveOnClose.reset();
  };
  const closeEditOnClose = () => {
    setEditingOnClose(null);
    saveOnClose.reset();
  };
  return (
    <Stack gap="xl">
      <PageHeader
        title={t("channels")}
        description={t("channelsDescription")}
        actions={<Button onClick={() => setOpened(true)}>{t("addChannel")}</Button>}
      />
      {providers.some((provider) => provider.warning === "tenant_wide") && (
        <Alert color="orange" title={t("tenantWideTitle")}>
          {t("tenantWide")}
        </Alert>
      )}
      {query.isError && (
        <Alert color="red">
          {t("couldNotLoadChannels")}: {query.error.message}
        </Alert>
      )}
      <Card withBorder radius="lg">
        {query.isPending ? (
          <ContentSkeleton rows={4} rowHeight={52} />
        ) : (
          <Table.ScrollContainer minWidth={800}>
            <Table highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("address")}</Table.Th>
                  <Table.Th>{t("provider")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  <Table.Th>{t("syncStatus")}</Table.Th>
                  <Table.Th>{t("actions")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {query.data?.map((channel) => (
                  <Table.Tr key={channel.id}>
                    <Table.Td>
                      <Text fw={600}>{channel.displayName || channel.address}</Text>
                      {channel.displayName && (
                        <Text size="xs" c="dimmed">
                          {channel.address}
                        </Text>
                      )}
                    </Table.Td>
                    <Table.Td>
                      <Badge variant="light">{providerLabel(channel.provider)}</Badge>
                      {channel.isDefault && <Badge ml="xs">{t("default")}</Badge>}
                    </Table.Td>
                    <Table.Td>
                      <Badge color={channel.isActive ? "teal" : "gray"}>
                        {channel.isActive ? t("active") : t("inactive")}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      <ReceiveStatus sync={channel.sync} />
                      {channel.provider === "entra" && channel.mailbox?.onClose === "read" && (
                        <Text size="xs" c="dimmed">
                          {t("onCloseSummaryRead")}
                        </Text>
                      )}
                      {channel.provider === "entra" && channel.mailbox?.onClose === "move" && (
                        <Text size="xs" c="dimmed">
                          {t("onCloseSummaryMove", { folder: channel.mailbox.folder || "Handled in Vantigo" })}
                        </Text>
                      )}
                    </Table.Td>
                    <Table.Td>
                      <Group gap="xs">
                        <Button
                          size="xs"
                          variant="subtle"
                          loading={verify.isPending && verify.variables?.id === channel.id}
                          onClick={() => verify.mutate(channel)}
                        >
                          {t("verify")}
                        </Button>
                        <Button
                          size="xs"
                          variant="subtle"
                          onClick={() => update.mutate({ id: channel.id, isActive: !channel.isActive })}
                        >
                          {channel.isActive ? t("deactivate") : t("activate")}
                        </Button>
                        {channel.provider === "entra" && (
                          <Button size="xs" variant="subtle" onClick={() => openEditOnClose(channel)}>
                            {t("editOnClose")}
                          </Button>
                        )}
                        {channel.provider === "smtp" && entra?.available && (
                          <Button size="xs" variant="subtle" onClick={() => setSwitching(channel)}>
                            {t("switchToMicrosoft365")}
                          </Button>
                        )}
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Card>
      <ChannelForm
        opened={opened}
        onClose={() => setOpened(false)}
        providers={providers}
        providerLabel={providerLabel}
        onCreated={() => {
          void refresh();
          notifications.show({ title: t("channelCreated"), message: t("emailChannelReady") });
        }}
      />
      <Modal opened={switching !== null} onClose={closeSwitch} title={t("switchToMicrosoft365")}>
        <Stack>
          <Text size="sm">{t("switchConfirm")}</Text>
          <Select
            label={t("importWindow")}
            allowDeselect={false}
            value={switchImportWindow}
            onChange={(value) => {
              if (value) setSwitchImportWindow(value as ChannelImportWindow);
            }}
            data={[
              { value: "from_now", label: t("importFromNow") },
              { value: "last_30_days", label: t("importLast30Days") },
              { value: "last_90_days", label: t("importLast90Days") },
            ]}
          />
          <Select
            label={t("onClose")}
            allowDeselect={false}
            value={switchOnClose}
            onChange={(value) => {
              if (value) setSwitchOnClose(value as ChannelOnClose);
            }}
            data={[
              { value: "none", label: t("onCloseNone") },
              { value: "read", label: t("onCloseRead") },
              { value: "move", label: t("onCloseMove") },
            ]}
          />
          {switchOnClose === "move" && (
            <TextInput
              required
              label={t("onCloseFolder")}
              value={switchFolder}
              onChange={(event) => setSwitchFolder(event.currentTarget.value)}
            />
          )}
          {switchProvider.error && <Alert color="red">{channelErrorMessage(switchProvider.error, t, locale)}</Alert>}
          <Group justify="end">
            <Button variant="default" onClick={closeSwitch}>
              {t("cancel")}
            </Button>
            <Button
              color="red"
              loading={switchProvider.isPending}
              onClick={() => switching && switchProvider.mutate(switching)}
            >
              {t("switch")}
            </Button>
          </Group>
        </Stack>
      </Modal>
      <Modal opened={editingOnClose !== null} onClose={closeEditOnClose} title={t("editOnClose")}>
        <Stack>
          <Select
            label={t("onClose")}
            allowDeselect={false}
            value={editOnCloseValue}
            onChange={(value) => {
              if (value) setEditOnCloseValue(value as ChannelOnClose);
            }}
            data={[
              { value: "none", label: t("onCloseNone") },
              { value: "read", label: t("onCloseRead") },
              { value: "move", label: t("onCloseMove") },
            ]}
          />
          {editOnCloseValue === "move" && (
            <TextInput
              required
              label={t("onCloseFolder")}
              value={editFolderValue}
              onChange={(event) => setEditFolderValue(event.currentTarget.value)}
            />
          )}
          {saveOnClose.error && <Alert color="red">{channelErrorMessage(saveOnClose.error, t, locale)}</Alert>}
          <Group justify="end">
            <Button variant="default" onClick={closeEditOnClose}>
              {t("cancel")}
            </Button>
            <Button
              loading={saveOnClose.isPending}
              onClick={() =>
                editingOnClose &&
                saveOnClose.mutate({
                  id: editingOnClose.id,
                  onClose: editOnCloseValue,
                  folder: editFolderValue,
                })
              }
            >
              {t("save")}
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  );
}
