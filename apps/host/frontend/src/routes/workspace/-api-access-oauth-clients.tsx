import { Alert, Button, Code, Group, Modal, Stack, Table, Text, Textarea, TextInput } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageSection, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  archiveRegisteredClient,
  getOAuthClientHosts,
  listRegisteredClients,
  oauthClientHostsQueryKey,
  type RegisteredClient,
  registerClient,
  registeredClientsQueryKey,
  setOAuthClientHosts,
  updateRegisteredClient,
} from "../../api/oauth";
import { codeOf, fieldErrorsOf } from "./-api-access-shared";
import { Copyable } from "./-api-key-dialog";

const statusOf = (error: unknown) => (error as { status?: number } | null)?.status;

/** A name the client was given, kept to its box however it is built. */
const clipped = { overflowWrap: "anywhere", overflow: "hidden" } as const;

type Draft = { id?: string; version?: string; name: string; redirectUris: string };

/** The entries of a form's text, one a line, blanks dropped. */
const redirectLines = (text: string) =>
  text
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");

/**
 * Workspace admin › API access › Registered clients (OAuth connections design
 * C8, C25): the clients an Owner registers by hand for an app that publishes
 * no metadata document, such as Cursor or VS Code. Registering one answers
 * its client ID, which the app is configured with; there is no secret. Edit
 * changes its name and redirect URIs; Archive ends its connections behind a
 * confirmation. While the installation has OAuth switched off the list
 * answers 404 and the section is not shown.
 */
export const RegisteredClients = () => {
  const { t, formatters } = useI18n("host");
  const queryClient = useQueryClient();
  const clients = useQuery({ queryKey: registeredClientsQueryKey, queryFn: listRegisteredClients, retry: false });
  const [draft, setDraft] = useState<Draft | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [created, setCreated] = useState<RegisteredClient | null>(null);

  const save = useMutation({
    mutationFn: (d: Draft) => {
      const body = { name: d.name.trim(), redirectUris: redirectLines(d.redirectUris) };
      return d.id && d.version ? updateRegisteredClient(d.id, d.version, body) : registerClient(body);
    },
    onSuccess: async (client, d) => {
      setDraft(null);
      if (!d.id) setCreated(client);
      notifications.show({
        color: "teal",
        title: t(d.id ? "apiAccess.saved" : "apiAccess.clientRegistered"),
        message: client.name,
      });
      await queryClient.invalidateQueries({ queryKey: registeredClientsQueryKey });
    },
    onError: async (error) => {
      setErrors(fieldErrorsOf(error));
      const stale = codeOf(error) === "version_mismatch";
      notifications.show({
        color: stale ? "orange" : "red",
        title: t("apiAccess.couldNotSave"),
        message: stale ? t("apiAccess.changedMeanwhile") : (error as Error).message,
      });
      if (stale) await queryClient.invalidateQueries({ queryKey: registeredClientsQueryKey });
    },
  });
  const archive = useMutation({
    mutationFn: (client: RegisteredClient) => archiveRegisteredClient(client.id, client.version),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("apiAccess.clientArchived"), message: "" });
      await queryClient.invalidateQueries({ queryKey: registeredClientsQueryKey });
    },
    onError: async (error) => {
      notifications.show({
        color: "red",
        title: t("apiAccess.couldNotArchiveClient"),
        message: (error as Error).message,
      });
      await queryClient.invalidateQueries({ queryKey: registeredClientsQueryKey });
    },
  });

  if (clients.isError && statusOf(clients.error) === 404) return null;
  const open = (d: Draft) => {
    setErrors({});
    setDraft(d);
  };
  const confirmArchive = (client: RegisteredClient) =>
    modals.openConfirmModal({
      title: (
        <Text fw={600} lineClamp={2} style={clipped}>
          {t("apiAccess.archiveClientTitle", { name: client.name })}
        </Text>
      ),
      children: <Text size="sm">{t("apiAccess.archiveClientBody", { count: client.liveConnections })}</Text>,
      labels: { confirm: t("apiAccess.archiveClient"), cancel: t("common.cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => archive.mutate(client),
    });

  return (
    <PageSection
      title={t("apiAccess.registeredClients")}
      actions={
        <Button
          variant="default"
          leftSection={<IconPlus size={16} />}
          onClick={() => open({ name: "", redirectUris: "" })}
        >
          {t("apiAccess.registerClient")}
        </Button>
      }
    >
      <Stack>
        <Text size="sm">{t("apiAccess.registeredClientsDescription")}</Text>
        {clients.isPending ? (
          <ContentSkeleton rows={2} />
        ) : clients.isError ? (
          <Alert color="red" title={t("apiAccess.couldNotLoadClients")}>
            {clients.error.message}
          </Alert>
        ) : clients.data.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("apiAccess.noClients")}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={860}>
            <Table verticalSpacing="sm" data-testid="registered-clients">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("apiAccess.name")}</Table.Th>
                  <Table.Th>{t("apiAccess.clientId")}</Table.Th>
                  <Table.Th>{t("apiAccess.redirectUris")}</Table.Th>
                  <Table.Th>{t("apiAccess.registeredBy")}</Table.Th>
                  <Table.Th>{t("apiAccess.connectedApps")}</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {clients.data.map((client) => (
                  <Table.Tr key={client.id} data-client={client.id}>
                    <Table.Td>
                      <Text fw={600} lineClamp={2} maw={240} style={clipped}>
                        {client.name}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Code>{client.clientId}</Code>
                    </Table.Td>
                    <Table.Td>
                      <Stack gap={2}>
                        {client.redirectUris.map((uri) => (
                          <Code key={uri} style={clipped}>
                            {uri}
                          </Code>
                        ))}
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      <Stack gap={2}>
                        <Text size="sm">{client.createdByName ?? t("apiAccess.deletedPerson")}</Text>
                        <Text size="xs" c="dimmed">
                          {formatters.formatDate(client.createdAt, { dateStyle: "medium" })}
                        </Text>
                      </Stack>
                    </Table.Td>
                    <Table.Td>{client.liveConnections}</Table.Td>
                    <Table.Td>
                      <Group gap={4} justify="flex-end" wrap="nowrap">
                        <Button
                          size="compact-sm"
                          variant="subtle"
                          aria-label={t("apiAccess.editClientLabel", { name: client.name })}
                          onClick={() =>
                            open({
                              id: client.id,
                              version: client.version,
                              name: client.name,
                              redirectUris: client.redirectUris.join("\n"),
                            })
                          }
                        >
                          {t("apiAccess.edit")}
                        </Button>
                        <Button
                          size="compact-sm"
                          variant="subtle"
                          color="red"
                          aria-label={t("apiAccess.archiveClientLabel", { name: client.name })}
                          onClick={() => confirmArchive(client)}
                        >
                          {t("apiAccess.archiveClient")}
                        </Button>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
      <Modal
        opened={draft !== null}
        onClose={() => setDraft(null)}
        title={t(draft?.id ? "apiAccess.editClient" : "apiAccess.registerClient")}
        centered
      >
        {draft && (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              save.mutate(draft);
            }}
          >
            <Stack>
              <TextInput
                label={t("apiAccess.name")}
                description={t("apiAccess.clientNameHint")}
                required
                maxLength={80}
                value={draft.name}
                error={errors.name}
                onChange={(event) => setDraft({ ...draft, name: event.currentTarget.value })}
              />
              <Textarea
                label={t("apiAccess.redirectUris")}
                description={t("apiAccess.redirectUrisHint")}
                required
                autosize
                minRows={2}
                value={draft.redirectUris}
                error={errors.redirectUris}
                onChange={(event) => setDraft({ ...draft, redirectUris: event.currentTarget.value })}
              />
              <Button
                type="submit"
                loading={save.isPending}
                disabled={!draft.name.trim() || redirectLines(draft.redirectUris).length === 0}
              >
                {t(draft.id ? "apiAccess.save" : "apiAccess.registerClient")}
              </Button>
            </Stack>
          </form>
        )}
      </Modal>
      <Modal
        opened={created !== null}
        onClose={() => setCreated(null)}
        title={t("apiAccess.clientRegistered")}
        centered
      >
        {created && (
          <Stack>
            <Copyable label={t("apiAccess.clientId")} value={created.clientId} />
            <Text size="sm">{t("apiAccess.clientRegisteredSteps")}</Text>
            <Button onClick={() => setCreated(null)}>{t("apiAccess.clientIdDone")}</Button>
          </Stack>
        )}
      </Modal>
    </PageSection>
  );
};

/**
 * Workspace admin › API access › Client hosts (OAuth connections design
 * C11): the hosts whose apps may connect by their metadata document. Empty
 * admits any; registered clients are always admitted. Saving never ends a
 * connection: one whose host is left out cannot renew its token until the
 * host is listed again. While OAuth is switched off the setting answers 404
 * and the section is not shown.
 */
export const ClientHosts = () => {
  const { t } = useI18n("host");
  const queryClient = useQueryClient();
  const hosts = useQuery({ queryKey: oauthClientHostsQueryKey, queryFn: getOAuthClientHosts, retry: false });
  // What the Owner typed, or null for the list as saved.
  const [draft, setDraft] = useState<string | null>(null);
  const [error, setError] = useState<string | undefined>();
  const text = draft ?? (hosts.data ?? []).join("\n");
  const value = redirectLines(text);
  const save = useMutation({
    mutationFn: (next: string[]) => setOAuthClientHosts(next),
    onSuccess: (saved) => {
      setError(undefined);
      queryClient.setQueryData(oauthClientHostsQueryKey, saved);
      setDraft(null);
      notifications.show({ color: "teal", title: t("apiAccess.saved"), message: "" });
    },
    onError: (err) => {
      setError(fieldErrorsOf(err).clientHosts);
      notifications.show({ color: "red", title: t("apiAccess.couldNotSave"), message: (err as Error).message });
    },
  });

  if (hosts.isError && statusOf(hosts.error) === 404) return null;
  const saved = hosts.data ?? [];
  const unchanged = value.length === saved.length && value.every((host, i) => host === saved[i]);
  return (
    <PageSection title={t("apiAccess.clientHosts")}>
      <Stack>
        <Text size="sm">{t("apiAccess.clientHostsDescription")}</Text>
        {hosts.isPending ? (
          <ContentSkeleton rows={1} />
        ) : hosts.isError ? (
          <Alert color="red" title={t("apiAccess.couldNotLoadClientHosts")}>
            {hosts.error.message}
          </Alert>
        ) : (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              save.mutate(value);
            }}
          >
            <Stack>
              <Textarea
                label={t("apiAccess.clientHostsLabel")}
                description={t(value.length === 0 ? "apiAccess.clientHostsAny" : "apiAccess.clientHostsOnly")}
                placeholder={t("apiAccess.clientHostsPlaceholder")}
                autosize
                minRows={2}
                value={text}
                onChange={(event) => setDraft(event.currentTarget.value)}
                error={error}
              />
              <Group>
                <Button type="submit" loading={save.isPending} disabled={unchanged}>
                  {t("apiAccess.save")}
                </Button>
              </Group>
            </Stack>
          </form>
        )}
      </Stack>
    </PageSection>
  );
};
