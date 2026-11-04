import { Badge, Button, Group, Stack, Table, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import {
  accountsQueryOptions,
  confirmAccountAdoption,
  type GoogleAccount,
  type GoogleConnection,
} from "../api/connection";
import { GOOGLE_QUERY_KEY } from "../api/request";
import { lastErrorText, refusalText, reportedText } from "../lib/words";
import { SectionCard } from "./step-card";

/** A user the module found holding the employee number rather than made, nobody has confirmed yet. */
const awaitsConfirmation = (account: GoogleAccount) => !account.createdByUs && !account.adoptionConfirmedAt;

const stateColor: Record<GoogleAccount["state"], string> = { enabled: "teal", disabled: "gray", deleted: "red" };

/**
 * The **Accounts** table (Google Workspace design G17): every user the module
 * made or found for a person in Staff — the address, the employee number,
 * the state, the licences it assigned, what it could not act on in words and
 * whether a first password waits to be shown. Staff decides who has an
 * account; the one thing done here is confirming a user the module found
 * rather than made — until then it writes nothing to it.
 */
export const AccountsCard = ({ connection }: { connection: GoogleConnection }) => {
  const { t } = useI18n("google");
  const accounts = useQuery(accountsQueryOptions());
  const queryClient = useQueryClient();
  const skuName = new Map(connection.skus.map((sku) => [sku.value, sku.name]));

  const confirm = useMutation({
    mutationFn: (account: GoogleAccount) => confirmAccountAdoption(account.userId),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("adoptionConfirmed"), message: t("adoptionConfirmedBody") });
      await queryClient.invalidateQueries({ queryKey: [GOOGLE_QUERY_KEY, "accounts"] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotConfirm"), message: refusalText(error, t) }),
  });

  const askConfirm = (account: GoogleAccount) =>
    modals.openConfirmModal({
      title: t("confirmAccountTitle", { email: account.primaryEmail }),
      children: <Text size="sm">{t("confirmAccountBody", { email: account.primaryEmail })}</Text>,
      labels: { confirm: t("confirmAdoption"), cancel: t("cancel") },
      onConfirm: () => confirm.mutate(account),
    });

  return (
    <SectionCard title={t("accounts")}>
      {accounts.isPending && <ContentSkeleton rows={2} rowHeight={24} />}
      {accounts.isError && (
        <Text size="sm" c="red">
          {t("accountsFailed")}
        </Text>
      )}
      {accounts.data?.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("noAccounts")}
        </Text>
      )}
      {accounts.data && accounts.data.length > 0 && (
        <Table.ScrollContainer minWidth={720}>
          <Table verticalSpacing={4} aria-label={t("accounts")}>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("accountEmail")}</Table.Th>
                <Table.Th>{t("accountEmployeeNumber")}</Table.Th>
                <Table.Th>{t("accountState")}</Table.Th>
                <Table.Th>{t("accountLicences")}</Table.Th>
                <Table.Th>{t("accountReported")}</Table.Th>
                <Table.Th>{t("accountPassword")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {accounts.data.map((account) => (
                <Table.Tr key={account.userId} c={account.state === "deleted" ? "dimmed" : undefined}>
                  <Table.Td>{account.primaryEmail}</Table.Td>
                  <Table.Td>{account.employeeNumber}</Table.Td>
                  <Table.Td>
                    <Stack gap={4} align="flex-start">
                      <Badge variant="light" color={stateColor[account.state]}>
                        {t(`accountState_${account.state}`)}
                      </Badge>
                      {awaitsConfirmation(account) ? (
                        <Group gap="xs" wrap="nowrap">
                          <Badge color="orange" variant="light">
                            {t("accountAwaitsConfirmation")}
                          </Badge>
                          <Button
                            size="compact-sm"
                            variant="light"
                            aria-label={t("confirmAccountNamed", { email: account.primaryEmail })}
                            loading={confirm.isPending && confirm.variables?.userId === account.userId}
                            onClick={() => askConfirm(account)}
                          >
                            {t("confirmAdoption")}
                          </Button>
                        </Group>
                      ) : (
                        !account.createdByUs && (
                          <Text size="xs" c="dimmed">
                            {t("accountAdopted")}
                          </Text>
                        )
                      )}
                    </Stack>
                  </Table.Td>
                  <Table.Td>
                    {account.licences.length > 0
                      ? account.licences.map((value) => skuName.get(value) ?? value).join(", ")
                      : t("none")}
                  </Table.Td>
                  <Table.Td>
                    <Stack gap={2}>
                      {account.lastError && (
                        <Text size="sm" c="red">
                          {lastErrorText(account.lastError, t)}
                        </Text>
                      )}
                      {account.reported.map((code) => (
                        <Text size="sm" c="orange" key={code}>
                          {reportedText(code, t)}
                        </Text>
                      ))}
                    </Stack>
                  </Table.Td>
                  <Table.Td>{account.passWaiting ? t("passWaiting") : ""}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
    </SectionCard>
  );
};
