import { Alert, Button } from "@mantine/core";
import { IconAlertCircle, IconBook } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { type AccountingMeta, metaQueryOptions } from "../api/accounting";
import { useAccountingFormat } from "../lib/format";
import { SETUP_ROUTE_PATH } from "../lib/routes";

/**
 * Every page but the overview and the setup is the books': until Set up the
 * books has run, the app shows only the way there (R12).
 */
export const BooksGate = ({ children }: { children: (meta: AccountingMeta) => ReactNode }) => {
  const { t } = useAccountingFormat();
  const meta = useQuery(metaQueryOptions());
  if (meta.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {meta.error.message}
      </Alert>
    );
  if (meta.isPending) return <ContentSkeleton rows={3} />;
  if (!meta.data.setUp) return <NotSetUp meta={meta.data} />;
  return <>{children(meta.data)}</>;
};

/** The books are not set up: the way to Set up the books, or who can. */
export const NotSetUp = ({ meta }: { meta: AccountingMeta }) => {
  const { t } = useAccountingFormat();
  return (
    <EmptyState
      icon={IconBook}
      title={t("notSetUp")}
      description={meta.canClose ? t("notSetUpHint") : t("notSetUpAsk")}
      action={
        meta.canClose && (
          <Button
            data-assist="accounting.setup.start"
            renderRoot={(props) => <Link to={SETUP_ROUTE_PATH} {...props} />}
          >
            {t("setUpTheBooks")}
          </Button>
        )
      }
    />
  );
};
