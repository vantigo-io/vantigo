import { Alert, Badge, Button, Card, Group, Loader, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { channelsQueryOptions, updateChannel, verifyChannel } from "../api/channels";
import { ChannelForm } from "../components/ChannelForm";
import "../i18n";
export function ChannelsPage() {
  const { t } = useI18n("communications");
  const query = useQuery(channelsQueryOptions());
  const client = useQueryClient();
  const [opened, setOpened] = useState(false);
  const refresh = () => client.invalidateQueries({ queryKey: ["channels"] });
  const update = useMutation({
    mutationFn: ({ id, isActive }: { id: string; isActive: boolean }) => updateChannel(id, { isActive }),
    onSuccess: () => void refresh(),
  });
  const verify = useMutation({
    mutationFn: verifyChannel,
    onSuccess: () => notifications.show({ title: t("channelVerified"), message: t("channelConnectionValid") }),
  });
  return (
    <Stack gap="xl">
      <PageHeader
        eyebrow={t("communications")}
        title={t("channels")}
        description={t("channelsDescription")}
        actions={<Button onClick={() => setOpened(true)}>{t("addChannel")}</Button>}
      />
      {query.isError && (
        <Alert color="red">
          {t("couldNotLoadChannels")}: {query.error.message}
        </Alert>
      )}
      <Card withBorder radius="lg">
        {query.isPending ? (
          <Loader />
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
                      <Badge variant="light">{channel.provider}</Badge>
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
                          loading={verify.isPending}
                          onClick={() => verify.mutate(channel.id)}
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
        onCreated={() => {
          void refresh();
          notifications.show({ title: t("channelCreated"), message: t("emailChannelReady") });
        }}
      />
    </Stack>
  );
}
