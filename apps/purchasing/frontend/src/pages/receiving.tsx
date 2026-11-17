import { Alert, Anchor, Button, Stack, Table } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { ordersToReceiveQueryOptions } from "../api/purchasing";
import { OrderStatusBadges } from "../components/common";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { usePurchasingFormat } from "../lib/format";
import { receivePurchaseOrderLinkOptions } from "../lib/routes";

/**
 * The orders to receive (purchasing design P30, A47): every sent or partially
 * received order with goods still to come, the earliest expected first, each
 * opening its receive screen. It is purchasing:receive's own page — someone
 * at the goods reception who holds no other Purchasing key finds what to
 * receive here — and, like the receive screen, it shows no prices.
 */
export const ReceivingPage = () => {
  const { t, date } = usePurchasingFormat();
  const list = useQuery(ordersToReceiveQueryOptions());
  return (
    <Stack gap="lg">
      <PageHeader title={t("receivingTitle")} description={t("receivingDescription")} />
      <PageSection>
        {list.isError && (
          <Alert color="red" title={t("couldNotLoad")}>
            {refusalMessage(list.error, t)}
          </Alert>
        )}
        {list.isPending && <ContentSkeleton rows={5} rowHeight={40} />}
        {list.data && list.data.items.length === 0 && (
          <EmptyState title={t("nothingToReceiveTitle")} description={t("nothingToReceiveDescription")} />
        )}
        {list.data && list.data.items.length > 0 && (
          <Table.ScrollContainer minWidth={640}>
            <Table verticalSpacing="xs" data-testid="receiving-list">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("number")}</Table.Th>
                  <Table.Th>{t("supplier")}</Table.Th>
                  <Table.Th>{t("site")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  <Table.Th>{t("expectedOn")}</Table.Th>
                  <Table.Th>{t("linesLeft")}</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {list.data.items.map((row) => (
                  <Table.Tr key={row.id} data-testid="receiving-row">
                    <Table.Td>
                      <Anchor renderRoot={(props) => <Link {...receivePurchaseOrderLinkOptions(row.id)} {...props} />}>
                        {row.number}
                      </Anchor>
                    </Table.Td>
                    <Table.Td>{row.supplierName}</Table.Td>
                    <Table.Td>{row.siteName}</Table.Td>
                    <Table.Td>
                      <OrderStatusBadges status={row.status} late={row.late} />
                    </Table.Td>
                    <Table.Td>{row.expectedOn ? date(row.expectedOn) : "—"}</Table.Td>
                    <Table.Td>{t("linesLeftOf", { open: row.openLines, total: row.goodsLines })}</Table.Td>
                    <Table.Td ta="right">
                      <Button
                        size="compact-sm"
                        variant="light"
                        renderRoot={(props) => <Link {...receivePurchaseOrderLinkOptions(row.id)} {...props} />}
                      >
                        {t("receive")}
                      </Button>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </PageSection>
    </Stack>
  );
};
