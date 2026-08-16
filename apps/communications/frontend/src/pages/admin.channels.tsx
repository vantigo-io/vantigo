import {
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Loader,
  Modal,
  PasswordInput,
  Select,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { channelsQueryOptions, createChannel, updateChannel, verifyChannel } from "../api/channels";
import "../i18n";
export function ChannelsPage() {
  const { t } = useI18n("communications");
  const query = useQuery(channelsQueryOptions());
  const client = useQueryClient();
  const [opened, setOpened] = useState(false);
  const [address, setAddress] = useState("");
  const [name, setName] = useState("");
  const [provider, setProvider] = useState<"smtp" | "mailgun">("smtp");
  const [smtpHost, setSmtpHost] = useState("");
  const [smtpPort, setSmtpPort] = useState("587");
  const [smtpUsername, setSmtpUsername] = useState("");
  const [smtpPassword, setSmtpPassword] = useState("");
  const [mailgunDomain, setMailgunDomain] = useState("");
  const [mailgunRegion, setMailgunRegion] = useState<"us" | "eu">("us");
  const [mailgunApiKey, setMailgunApiKey] = useState("");
  const [inboundSigningKey, setInboundSigningKey] = useState("");
  const refresh = () => client.invalidateQueries({ queryKey: ["channels"] });
  const create = useMutation({
    mutationFn: () =>
      createChannel({
        type: "email",
        address: address.trim(),
        displayName: name.trim() || undefined,
        provider,
        ...(provider === "smtp"
          ? {
              smtp: {
                host: smtpHost.trim(),
                port: Number(smtpPort),
                useSsl: true,
                username: smtpUsername.trim() || undefined,
                password: smtpPassword || undefined,
              },
            }
          : {
              mailgun: {
                domain: mailgunDomain.trim(),
                region: mailgunRegion,
                apiKey: mailgunApiKey,
                inboundSigningKey,
              },
            }),
      }),
    onSuccess: () => {
      setOpened(false);
      setAddress("");
      setName("");
      setProvider("smtp");
      setSmtpHost("");
      setSmtpPort("587");
      setSmtpUsername("");
      setSmtpPassword("");
      setMailgunDomain("");
      setMailgunApiKey("");
      setInboundSigningKey("");
      void refresh();
      notifications.show({ title: t("channelCreated"), message: t("emailChannelReady") });
    },
  });
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
      <Modal opened={opened} onClose={() => setOpened(false)} title={t("addEmailChannel")}>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            create.mutate();
          }}
        >
          <Stack>
            <TextInput
              required
              type="email"
              label={t("emailAddress")}
              value={address}
              onChange={(event) => setAddress(event.currentTarget.value)}
            />
            <TextInput label={t("displayName")} value={name} onChange={(event) => setName(event.currentTarget.value)} />
            <Select
              label={t("provider")}
              value={provider}
              data={[
                { value: "smtp", label: t("smtp") },
                { value: "mailgun", label: t("mailgun") },
              ]}
              onChange={(value) => value && setProvider(value as "smtp" | "mailgun")}
            />
            {provider === "smtp" ? (
              <>
                <TextInput
                  required
                  label={t("smtpHost")}
                  value={smtpHost}
                  onChange={(event) => setSmtpHost(event.currentTarget.value)}
                />
                <TextInput
                  required
                  label={t("smtpPort")}
                  value={smtpPort}
                  onChange={(event) => setSmtpPort(event.currentTarget.value)}
                />
                <TextInput
                  label={t("username")}
                  value={smtpUsername}
                  onChange={(event) => setSmtpUsername(event.currentTarget.value)}
                />
                <PasswordInput
                  label={t("password")}
                  value={smtpPassword}
                  onChange={(event) => setSmtpPassword(event.currentTarget.value)}
                />
              </>
            ) : (
              <>
                <TextInput
                  required
                  label={t("mailgunDomain")}
                  value={mailgunDomain}
                  onChange={(event) => setMailgunDomain(event.currentTarget.value)}
                />
                <Select
                  required
                  label={t("region")}
                  value={mailgunRegion}
                  data={[
                    { value: "us", label: t("us") },
                    { value: "eu", label: t("eu") },
                  ]}
                  onChange={(value) => value && setMailgunRegion(value as "us" | "eu")}
                />
                <PasswordInput
                  required
                  label={t("apiKey")}
                  value={mailgunApiKey}
                  onChange={(event) => setMailgunApiKey(event.currentTarget.value)}
                />
                <PasswordInput
                  required
                  label={t("inboundSigningKey")}
                  value={inboundSigningKey}
                  onChange={(event) => setInboundSigningKey(event.currentTarget.value)}
                />
              </>
            )}
            <Button type="submit" loading={create.isPending}>
              {t("createChannel")}
            </Button>
          </Stack>
        </form>
      </Modal>
    </Stack>
  );
}
