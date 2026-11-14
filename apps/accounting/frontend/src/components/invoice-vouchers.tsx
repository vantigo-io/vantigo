import { Anchor, Button, Group, Text } from "@mantine/core";
import { IconBook } from "@tabler/icons-react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import { useState } from "react";
import { vouchersQueryOptions } from "../api/accounting";
import { useAccountingFormat } from "../lib/format";
import { voucherPath } from "../lib/routes";

/**
 * An invoice's vouchers in the books (A13): the voucher it posted as and
 * those of its payments, credit notes and write-offs, each a link; on a
 * credit note's page, the credit note's own. Past the first page a button
 * shows how many more there are and reads them (L3). The host shows it on an
 * invoice's page when Accounting is on; it says nothing when the books are
 * not set up, the caller may not read them, or nothing is posted yet.
 */
export const InvoiceVouchers = ({ documentId }: { documentId: number }) => {
  const { t } = useAccountingFormat();
  const [limit, setLimit] = useState(FIRST_PAGE);
  const vouchers = useQuery({
    ...vouchersQueryOptions({ sourceDocumentId: documentId, limit }),
    retry: false,
    placeholderData: keepPreviousData,
  });
  if (!vouchers.data || vouchers.data.items.length === 0) return null;
  const more = vouchers.data.total - vouchers.data.items.length;
  return (
    <Group gap="xs" data-testid="invoice-vouchers">
      <IconBook size={16} />
      <Text size="sm">{t("postedInAccounting")}</Text>
      {vouchers.data.items.map((v) => (
        <Anchor key={v.id} size="sm" href={appUrl(voucherPath(v.id))}>
          {v.display}
        </Anchor>
      ))}
      {more > 0 &&
        (limit < MAX_PAGE ? (
          <Button variant="subtle" size="compact-sm" onClick={() => setLimit(MAX_PAGE)}>
            {t("vouchersMore", { count: more })}
          </Button>
        ) : (
          <Text size="sm" c="dimmed">
            {t("vouchersMore", { count: more })}
          </Text>
        ))}
    </Group>
  );
};

/** The first page of an invoice's vouchers, and the most one page answers. */
const FIRST_PAGE = 20;
const MAX_PAGE = 200;
