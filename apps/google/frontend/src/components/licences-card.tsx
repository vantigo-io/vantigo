import { Alert, Button, Group, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type GoogleConnection, readSkus } from "../api/connection";
import { GOOGLE_QUERY_KEY } from "../api/request";
import { refusalText } from "../lib/words";
import { SectionCard } from "./step-card";

/**
 * The **Licences** card: the licences the organisation holds as the last
 * read kept them on the connection — the choices of a Staff group's
 * licences — and a button that reads them from Google again. A refusal
 * (503 `credential_invalid` or `google_unreachable`, 429) is said in words.
 */
export const LicencesCard = ({ connection }: { connection: GoogleConnection }) => {
  const { t, formatters } = useI18n("google");
  const queryClient = useQueryClient();
  const read = useMutation({
    mutationFn: readSkus,
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("licencesRead"), message: t("licencesReadBody") });
      await queryClient.invalidateQueries({ queryKey: [GOOGLE_QUERY_KEY, "connection"] });
    },
  });
  const canRead = connection.status !== "unconfigured";

  return (
    <SectionCard title={t("licences")}>
      <Text size="sm">{t("licencesBody")}</Text>
      {connection.skus.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("licencesNone")}
        </Text>
      ) : (
        <Table.ScrollContainer minWidth={480}>
          <Table verticalSpacing={4} aria-label={t("licences")}>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("licenceName")}</Table.Th>
                <Table.Th>{t("licenceValue")}</Table.Th>
                <Table.Th>{t("licenceAssigned")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {connection.skus.map((sku) => (
                <Table.Tr key={sku.value}>
                  <Table.Td>{sku.name}</Table.Td>
                  <Table.Td>
                    <Text size="sm" ff="monospace">
                      {sku.value}
                    </Text>
                  </Table.Td>
                  <Table.Td>{formatters.formatNumber(sku.assigned)}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
      {read.isError && (
        <Alert color="red" variant="light" title={t("couldNotReadLicences")}>
          {refusalText(read.error, t)}
        </Alert>
      )}
      {!canRead && (
        <Text size="sm" c="dimmed">
          {t("licencesNeedConnection")}
        </Text>
      )}
      <Group justify="flex-end">
        <Button variant="light" loading={read.isPending} disabled={!canRead} onClick={() => read.mutate()}>
          {t("readLicences")}
        </Button>
      </Group>
    </SectionCard>
  );
};
