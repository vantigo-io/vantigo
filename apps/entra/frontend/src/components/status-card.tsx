import { Alert, Badge, Button, Group, List, SimpleGrid, Stack, Table, Text, Title } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import {
  accountsQueryOptions,
  confirmAdoption,
  type EntraAccount,
  type EntraConnection,
  eventsQueryOptions,
} from "../api/connection";
import { ENTRA_QUERY_KEY } from "../api/request";
import { actionText, codeText, reasonText, refusalText } from "../lib/words";
import { CredentialList } from "./credentials";
import { MissingPermission, SectionCard } from "./step-card";

const Fact = ({ label, value }: { label: string; value: string }) => (
  <Stack gap={0}>
    <Text size="xs" c="dimmed">
      {label}
    </Text>
    <Text size="sm">{value}</Text>
  </Stack>
);

const Subheading = ({ children }: { children: string }) => (
  <Title order={4} fz="h6" mt="xs">
    {children}
  </Title>
);

/** An account the module found holding the employee number rather than created, nobody has confirmed yet. */
const awaitsConfirmation = (account: EntraAccount) => !account.createdByUs && !account.adoptionConfirmed;

/**
 * The **Status** card (Entra design D15), shown once the connection is more
 * than unconfigured: the organisation, the permissions by name, the secrets,
 * the last check, the accounts the module keeps and its recent events. An
 * account the module found rather than created reads *Adopted — confirm*:
 * the module writes nothing to it until somebody confirms it is the person's.
 */
export const StatusCard = ({ connection, owner }: { connection: EntraConnection; owner: boolean }) => {
  const { t, formatters } = useI18n("entra");
  const events = useQuery(eventsQueryOptions());
  const accounts = useQuery({ ...accountsQueryOptions(), enabled: connection.accounts.count > 0 });
  const queryClient = useQueryClient();

  const confirm = useMutation({
    mutationFn: (account: EntraAccount) => confirmAdoption(account.objectId),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("adoptionConfirmed"), message: t("adoptionConfirmedBody") });
      await queryClient.invalidateQueries({ queryKey: [ENTRA_QUERY_KEY] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotConfirm"), message: refusalText(error, t) }),
  });

  const askConfirm = (account: EntraAccount) =>
    modals.openConfirmModal({
      title: t("confirmAdoptionTitle", { upn: account.userPrincipalName }),
      children: <Text size="sm">{t("confirmAdoptionBody", { upn: account.userPrincipalName })}</Text>,
      labels: { confirm: t("confirmAdoption"), cancel: t("cancel") },
      onConfirm: () => confirm.mutate(account),
    });
  const dateTime = (value: string) =>
    formatters.formatDate(new Date(value), { dateStyle: "medium", timeStyle: "short" });
  const connected = connection.status === "connected";

  return (
    <SectionCard
      title={t("status")}
      badge={
        <Badge color={connected ? "teal" : "red"} size="lg">
          {connected ? t("statusConnected") : t("statusFailing")}
        </Badge>
      }
    >
      {connection.statusReason && (
        <Alert color={connected ? "yellow" : "red"} variant="light">
          {reasonText(connection.statusReason, t)}
        </Alert>
      )}
      <SimpleGrid cols={{ base: 1, sm: 2, md: 4 }}>
        <Fact label={t("organisation")} value={connection.organisationName ?? t("notReadYet")} />
        <Fact
          label={t("lastChecked")}
          value={connection.lastCheckedAt ? dateTime(connection.lastCheckedAt) : t("notTestedYet")}
        />
        <Fact
          label={t("accounts")}
          value={t("accountCounts", {
            count: connection.accounts.count,
            enabled: connection.accounts.enabled,
            disabled: connection.accounts.disabled,
            deleted: connection.accounts.deleted,
          })}
        />
        <Fact label={t("groups")} value={t("groupsCount", { count: connection.groups.count })} />
      </SimpleGrid>

      <Subheading>{t("permissions")}</Subheading>
      {connection.permissionsMissing.length === 0 ? (
        <Text size="sm">{t("permissionsAllPresent")}</Text>
      ) : (
        <Stack gap={4}>
          <Text size="sm" c="red">
            {t("permissionsMissingList", { count: connection.permissionsMissing.length })}
          </Text>
          <List size="sm" aria-label={t("permissionsMissing")}>
            {connection.permissionsMissing.map((name) => (
              <List.Item key={name}>
                <MissingPermission name={name} />
              </List.Item>
            ))}
          </List>
        </Stack>
      )}
      {connection.permissionsPresent.length > 0 && (
        <Text size="sm" c="dimmed">
          {t("permissionsPresentList", { names: connection.permissionsPresent.join(", ") })}
        </Text>
      )}
      {connection.permissionsUnexpected.length > 0 && (
        // A warning, never a status (Outlook connection design M19): the connection stays connected.
        <Alert color="orange" variant="light" title={t("permissionsUnexpected")}>
          <Stack gap={4}>
            <List size="sm" aria-label={t("permissionsUnexpected")}>
              {connection.permissionsUnexpected.map((name) => (
                <List.Item key={name}>{name}</List.Item>
              ))}
            </List>
            <Text size="sm">{t("permissionsUnexpectedHint")}</Text>
          </Stack>
        </Alert>
      )}

      <Subheading>{t("secrets")}</Subheading>
      <CredentialList credentials={connection.credentials} owner={owner} />

      {connection.accounts.count > 0 && (
        <>
          <Subheading>{t("accountsList")}</Subheading>
          {accounts.isPending && <ContentSkeleton rows={2} rowHeight={24} />}
          {accounts.data && (
            <Table.ScrollContainer minWidth={480}>
              <Table verticalSpacing={4} aria-label={t("accountsList")}>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("accountUpn")}</Table.Th>
                    <Table.Th>{t("accountState")}</Table.Th>
                    <Table.Th>{t("accountReported")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {accounts.data.map((account) => (
                    <Table.Tr key={account.objectId}>
                      <Table.Td>{account.userPrincipalName}</Table.Td>
                      <Table.Td>
                        {awaitsConfirmation(account) ? (
                          <Stack gap={4} align="flex-start">
                            <Text size="sm">{t(`accountState_${account.state}`)}</Text>
                            <Group gap="xs" wrap="nowrap">
                              <Badge color="orange" variant="light">
                                {t("accountAdopted")}
                              </Badge>
                              <Button
                                size="compact-sm"
                                variant="light"
                                aria-label={t("confirmAdoptionNamed", { upn: account.userPrincipalName })}
                                loading={confirm.isPending && confirm.variables?.objectId === account.objectId}
                                onClick={() => askConfirm(account)}
                              >
                                {t("confirmAdoption")}
                              </Button>
                            </Group>
                          </Stack>
                        ) : (
                          t(`accountState_${account.state}`)
                        )}
                      </Table.Td>
                      <Table.Td>{account.reported.map((code) => codeText(code, t)).join("; ")}</Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
        </>
      )}

      <Subheading>{t("recentEvents")}</Subheading>
      {events.isPending && <ContentSkeleton rows={2} rowHeight={24} />}
      {events.isError && (
        <Text size="sm" c="red">
          {t("eventsFailed")}
        </Text>
      )}
      {events.data && events.data.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("noEvents")}
        </Text>
      )}
      {events.data && events.data.length > 0 && (
        <Stack
          gap={6}
          component="ul"
          aria-label={t("recentEvents")}
          style={{ listStyle: "none", padding: 0, margin: 0 }}
        >
          {events.data.map((event) => (
            <Group key={event.id} component="li" gap="xs" wrap="nowrap" align="flex-start">
              <Text size="xs" c="dimmed" style={{ whiteSpace: "nowrap" }}>
                {dateTime(event.at)}
              </Text>
              <Stack gap={0}>
                <Text size="sm">
                  {actionText(event.action, t)}
                  {event.upn ? ` · ${event.upn}` : ""}
                  {event.actor.startsWith("system:") ? ` · ${t("bySystem")}` : ""}
                </Text>
                {event.codes.length > 0 && (
                  <Text size="xs" c="orange">
                    {event.codes.map((code) => codeText(code, t)).join("; ")}
                  </Text>
                )}
              </Stack>
            </Group>
          ))}
        </Stack>
      )}
    </SectionCard>
  );
};
