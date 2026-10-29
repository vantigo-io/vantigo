import { Alert, Badge, Button, Card, Group, Modal, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type Channel,
  channelProvidersQueryOptions,
  channelsQueryOptions,
  switchChannelProvider,
  updateChannel,
  verifyChannel,
} from "../api/channels";
import { ChannelForm } from "../components/ChannelForm";
import { channelErrorMessage } from "../components/channelErrors";
import "../i18n";
export function ChannelsPage() {
  const { t, locale } = useI18n("communications");
  const query = useQuery(channelsQueryOptions());
  const providersQuery = useQuery(channelProvidersQueryOptions());
  const providers = providersQuery.data ?? [];
  const entra = providers.find((provider) => provider.key === "entra");
  const client = useQueryClient();
  const [opened, setOpened] = useState(false);
  const [switching, setSwitching] = useState<Channel | null>(null);
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
  const verify = useMutation({
    mutationFn: (channel: Channel) => verifyChannel(channel.id),
    onSuccess: () => notifications.show({ title: t("channelVerified"), message: t("channelConnectionValid") }),
    onError: (error, channel) =>
      notifications.show({
        color: "red",
        title: t("channelNotVerified"),
        message: channelErrorMessage(error, t, channel.provider, locale, "errorVerificationFailed"),
      }),
  });
  const switchProvider = useMutation({
    mutationFn: (channel: Channel) => switchChannelProvider(channel.id),
    onSuccess: () => {
      setSwitching(null);
      void refresh();
      notifications.show({ title: t("channelSwitched"), message: t("channelSwitchedMessage") });
    },
  });
  const closeSwitch = () => {
    setSwitching(null);
    switchProvider.reset();
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
          <Table.ScrollContainer minWidth={650}>
            <Table highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("address")}</Table.Th>
                  <Table.Th>{t("provider")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
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
          {switchProvider.error && (
            <Alert color="red">{channelErrorMessage(switchProvider.error, t, "entra", locale)}</Alert>
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
    </Stack>
  );
}
