import { Alert, Badge, Button, Group, Modal, Select, Stack, Table, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader, PageSection, useI18n } from "@vantigo/frontend-shell";
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
import { isMailboxProvider } from "../components/channelProviders";
import { relative } from "../components/conversationHelpers";
import "../i18n";

const syncErrors = ["mailbox_denied", "mailbox_gone", "mailbox_unknown", "provider_unavailable", "sync_failed"];

/** Whether a channel receives: a mailbox channel's sync, by its state and last failure; SMTP sends only. */
function ReceiveStatus({ sync, provider }: { sync: ChannelSync | null | undefined; provider: string }) {
  const { t } = useI18n("communications");
  if (!sync)
    return (
      <Text size="sm" c="dimmed">
        {t("sendsOnly")}
      </Text>
    );
  const reason = sync.error
    ? t(`syncError.${syncErrors.includes(sync.error) ? sync.error : "sync_failed"}`, { provider })
    : t("syncError.sync_failed", { provider });
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
  // The mailbox providers an SMTP channel can be switched to now.
  const switchTargets = providers.filter((provider) => isMailboxProvider(provider.key) && provider.available);
  const client = useQueryClient();
  const [opened, setOpened] = useState(false);
  const [switching, setSwitching] = useState<{ channel: Channel; provider: string } | null>(null);
  const [switchImportWindow, setSwitchImportWindow] = useState<ChannelImportWindow>("from_now");
  const [switchOnClose, setSwitchOnClose] = useState<ChannelOnClose>("none");
  const [switchFolder, setSwitchFolder] = useState("Handled in Vantigo");
  const [editingOnClose, setEditingOnClose] = useState<Channel | null>(null);
  const [editOnCloseValue, setEditOnCloseValue] = useState<ChannelOnClose>("none");
  const [editFolderValue, setEditFolderValue] = useState("Handled in Vantigo");
  const refresh = () => client.invalidateQueries({ queryKey: ["channels"] });
  // The badge and the select name a provider as the provider names itself;
  // a channel whose provider is not offered now still reads as a name.
  // A provider no longer listed is named by its key — Entra's, the one key
  // channels had before the providers became many, by its catalog name.
  const providerLabel = (key: string) =>
    providers.find((provider) => provider.key === key)?.displayName ??
    (key === "entra" ? t("microsoft365") : isMailboxProvider(key) ? key : t("smtp"));
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
    onError: (error, channel) =>
      notifications.show({
        color: "red",
        title: t("channelNotVerified"),
        message: channelErrorMessage(error, t, locale, "errorVerificationFailed", providerLabel(channel.provider)),
      }),
  });
  const switchProvider = useMutation({
    mutationFn: ({ channel, provider }: { channel: Channel; provider: string }) => {
      const opts: SwitchChannelProviderOptions = {
        ...(switchImportWindow !== "from_now" ? { importWindow: switchImportWindow } : {}),
        ...(switchOnClose !== "none" ? { onClose: switchOnClose } : {}),
        ...(switchOnClose === "move" && switchFolder.trim() ? { folder: switchFolder.trim() } : {}),
      };
      return Object.keys(opts).length > 0
        ? switchChannelProvider(channel.id, provider, opts)
        : switchChannelProvider(channel.id, provider);
    },
    onSuccess: (_, { provider }) => {
      setSwitching(null);
      setSwitchImportWindow("from_now");
      setSwitchOnClose("none");
      setSwitchFolder("Handled in Vantigo");
      void refresh();
      notifications.show({
        title: t("channelSwitched"),
        message: t("channelSwitchedMessage", { provider: providerLabel(provider) }),
      });
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
      <PageSection>
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
                      {channel.available === false && (
                        <>
                          <Badge ml="xs" color="orange">
                            {t("unavailable")}
                          </Badge>
                          <Text size="xs" c="dimmed">
                            {isMailboxProvider(channel.provider)
                              ? t("unavailableMailbox", { provider: providerLabel(channel.provider) })
                              : t("unavailableSmtp")}
                          </Text>
                        </>
                      )}
                    </Table.Td>
                    <Table.Td>
                      <ReceiveStatus sync={channel.sync} provider={providerLabel(channel.provider)} />
                      {isMailboxProvider(channel.provider) && channel.mailbox?.onClose === "read" && (
                        <Text size="xs" c="dimmed">
                          {t("onCloseSummaryRead")}
                        </Text>
                      )}
                      {isMailboxProvider(channel.provider) && channel.mailbox?.onClose === "move" && (
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
                        {isMailboxProvider(channel.provider) && (
                          <Button size="xs" variant="subtle" onClick={() => openEditOnClose(channel)}>
                            {t("editOnClose")}
                          </Button>
                        )}
                        {!isMailboxProvider(channel.provider) &&
                          switchTargets.map((target) => (
                            <Button
                              key={target.key}
                              size="xs"
                              variant="subtle"
                              onClick={() => setSwitching({ channel, provider: target.key })}
                            >
                              {t("switchToProvider", { provider: target.displayName })}
                            </Button>
                          ))}
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </PageSection>
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
      <Modal
        opened={switching !== null}
        onClose={closeSwitch}
        title={t("switchToProvider", { provider: switching ? providerLabel(switching.provider) : "" })}
      >
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
          {switchProvider.error && (
            <Alert color="red">
              {channelErrorMessage(
                switchProvider.error,
                t,
                locale,
                undefined,
                switching ? providerLabel(switching.provider) : "",
              )}
            </Alert>
          )}
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
          {saveOnClose.error && (
            <Alert color="red">
              {channelErrorMessage(
                saveOnClose.error,
                t,
                locale,
                undefined,
                editingOnClose ? providerLabel(editingOnClose.provider) : "",
              )}
            </Alert>
          )}
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
