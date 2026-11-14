import { Anchor, Group, Text } from "@mantine/core";
import { IconBook } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import { vouchersQueryOptions } from "../api/accounting";
import { useAccountingFormat } from "../lib/format";
import { voucherPath } from "../lib/routes";

/**
 * An invoice's vouchers in the books (A13): the voucher it posted as and
 * those of its payments, credit notes and write-offs, each a link. The host
 * shows it on an invoice's page when Accounting is on; it says nothing when
 * the books are not set up, the caller may not read them, or nothing is
 * posted yet.
 */
export const InvoiceVouchers = ({ documentId }: { documentId: number }) => {
  const { t } = useAccountingFormat();
  const vouchers = useQuery({ ...vouchersQueryOptions({ sourceDocumentId: documentId, limit: 50 }), retry: false });
  if (!vouchers.data || vouchers.data.items.length === 0) return null;
  return (
    <Group gap="xs" data-testid="invoice-vouchers">
      <IconBook size={16} />
      <Text size="sm">{t("postedInAccounting")}</Text>
      {vouchers.data.items.map((v) => (
        <Anchor key={v.id} size="sm" href={appUrl(voucherPath(v.id))}>
          {v.display}
        </Anchor>
      ))}
    </Group>
  );
};
