import { Alert } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { invoicesMetaQueryOptions } from "../api/meta";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

/**
 * A run's page and the paper letters fall under the nav's `/invoices` entry,
 * `invoices:access`, while every request of theirs needs `invoices:payments`
 * (D1): meta's `canRunReminders` is read first, and without it the page says
 * it is not allowed — its children, and so their requests, are never mounted,
 * so no read of theirs answers 403.
 */
export const RemindersGate = ({ children }: { children: ReactNode }) => {
  const { t, date } = useInvoiceFormat();
  const meta = useQuery(invoicesMetaQueryOptions());
  if (meta.isPending) return <ContentSkeleton rows={4} rowHeight={40} />;
  if (meta.isError)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMeta")}>
        {refusalMessage(meta.error, t, date)}
      </Alert>
    );
  if (!meta.data.capabilities.canRunReminders)
    return (
      <Alert color="gray" icon={<IconAlertCircle size={16} />} title={t("reminders.notAllowedTitle")}>
        {t("reminders.notAllowed")}
      </Alert>
    );
  return children;
};
