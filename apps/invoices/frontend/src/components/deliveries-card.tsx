import { Card, Stack, Table, Text, Title } from "@mantine/core";
import type { InvoiceDelivery } from "../api/send";
import "../i18n";
import { useInvoiceFormat } from "../lib/format";

/**
 * Every e-mail that handed an issued document over (D4, D10): when it went,
 * to whom and under which subject. The address is the server's to give — only
 * a caller with `invoices:issue` gets it — so a reader sees no address column
 * at all; one the anonymisation blanked says "(anonymised)".
 */
export const DeliveriesCard = ({ deliveries }: { deliveries: InvoiceDelivery[] }) => {
  const { t, dateTime } = useInvoiceFormat();
  const withAddress = deliveries.some((d) => d.recipient !== undefined);
  return (
    <Card withBorder data-testid="deliveries-card">
      <Stack gap="xs">
        <Title order={4}>{t("deliveries")}</Title>
        {deliveries.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("notSentYet")}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={480}>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("sentAt")}</Table.Th>
                  {withAddress && <Table.Th>{t("sentToColumn")}</Table.Th>}
                  <Table.Th>{t("subject")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {deliveries.map((d) => (
                  <Table.Tr key={d.id}>
                    <Table.Td>{dateTime(d.sentAt)}</Table.Td>
                    {withAddress && (
                      <Table.Td>
                        {d.recipient === "" ? (
                          <Text size="sm" c="dimmed" component="span">
                            {t("anonymised")}
                          </Text>
                        ) : (
                          d.recipient
                        )}
                      </Table.Td>
                    )}
                    <Table.Td>{d.subject}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
    </Card>
  );
};
