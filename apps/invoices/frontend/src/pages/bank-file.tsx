import { Alert, Anchor, Stack, Text, Title } from "@mantine/core";
import { IconAlertCircle, IconArrowLeft } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { appUrl, ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { bankFileQueryOptions } from "../api/bank";
import { invoicesMetaQueryOptions } from "../api/meta";
import { BankTransactionTable } from "../components/bank-transaction-table";
import "../i18n";
import { accountNumber, useWho } from "../lib/bank";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { PAYMENTS_ROUTE_PATH } from "../lib/routes";
import { ImportResultCard } from "./payments";

export interface BankFilePageProps {
  bankFileId: number;
  /** The signed-in user's id, so an import or an event of theirs says "you". */
  currentUserId?: string;
}

/**
 * One imported bank file (D3, D22): its format, when and by whom it was
 * uploaded, the accounts and booking days it covers, its lines counted by
 * kind — with "Match the rest" while some are not yet matched — and every line
 * it brought, duplicates included, with the queue's actions.
 */
export const BankFilePage = ({ bankFileId, currentUserId }: BankFilePageProps) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const who = useWho(currentUserId);
  const navigate = useNavigate() as (options: unknown) => void;
  const meta = useQuery(invoicesMetaQueryOptions());
  const detail = useQuery(bankFileQueryOptions(bankFileId));
  const currency = meta.data?.currency ?? "NOK";
  const canAct = Boolean(meta.data?.capabilities.canImportBankFiles);
  const file = detail.data?.file;
  return (
    <Stack gap="lg">
      <Anchor
        href={appUrl(PAYMENTS_ROUTE_PATH)}
        size="sm"
        onClick={(event) => {
          if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
          event.preventDefault();
          navigate({ to: PAYMENTS_ROUTE_PATH });
        }}
      >
        <IconArrowLeft size={14} /> {t("bank.backToPayments")}
      </Anchor>
      <PageHeader
        title={t("bank.fileTitle", { id: bankFileId })}
        description={
          file
            ? t("bank.fileUploaded", {
                format: t(`bank.format.${file.format}`),
                at: dateTime(file.uploadedAt),
                who: who(file.uploadedBy),
              })
            : undefined
        }
      />
      {detail.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("bank.failedToLoad")}>
          {refusalMessage(detail.error, t, date)}
        </Alert>
      )}
      {(detail.isPending || meta.isPending) && <ContentSkeleton rows={4} rowHeight={40} />}
      {file && detail.data && meta.data && (
        <>
          <Stack gap={2}>
            <Text size="sm">{t("bank.accountsInFile", { accounts: file.accounts.map(accountNumber).join(", ") })}</Text>
            {file.firstBookedOn && file.lastBookedOn && (
              <Text size="sm">
                {t("bank.bookingDays")}:{" "}
                {t("bank.bookingDaysRange", { from: date(file.firstBookedOn), to: date(file.lastBookedOn) })}
              </Text>
            )}
          </Stack>
          <ImportResultCard file={file} currency={currency} canAct={canAct} />
          <Title order={4}>{t("bank.lines")}</Title>
          <BankTransactionTable
            lines={detail.data.transactions}
            currency={currency}
            canAct={canAct}
            currentUserId={currentUserId}
          />
        </>
      )}
    </Stack>
  );
};
