import { Alert, Stack } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { invoicesMetaQueryOptions } from "../api/meta";
import "../i18n";

/**
 * The Invoices app's home. This delivery's first task mounts the app and
 * nothing more: the list, the editor, settings and the journal come with the
 * operations behind them.
 */
export const InvoicesPage = () => {
  const { t } = useI18n("invoices");
  const meta = useQuery(invoicesMetaQueryOptions());

  return (
    <Stack gap="lg">
      <PageHeader title={t("invoices")} description={t("invoicesDescription")} />
      {meta.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMeta")}>
          {meta.error.message}
        </Alert>
      )}
      {meta.isPending && <ContentSkeleton rows={3} rowHeight={52} />}
      {meta.data && <EmptyState title={t("noInvoices")} description={t("noInvoicesDescription")} />}
    </Stack>
  );
};
