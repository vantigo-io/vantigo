import { Alert, Anchor, List } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { bankStatusQueryOptions } from "../api/accounting";
import { useAccountingFormat } from "../lib/format";
import { bankAccountPath } from "../lib/routes";

/**
 * What the bank's reconciliation leaves open through a day (A28): each bank
 * account with lines not matched, or a statement balance that differs from
 * the ledger's. Locking a month asks for a confirmation over it; closing a VAT
 * term, which moves the lock too, only shows it. Nothing when all is matched.
 */
export const BankWarning = ({ through }: { through: string }) => {
  const { t, day, money } = useAccountingFormat();
  const status = useQuery(bankStatusQueryOptions(through));
  if (!status.data || status.data.reconciled) return null;
  return (
    <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title={t("bankNotReconciled", { day: day(through) })}>
      <List size="sm">
        {status.data.accounts
          .filter((a) => !a.reconciled)
          .map((a) => (
            <List.Item key={a.bankAccountId}>
              <Anchor component={Link} to={bankAccountPath(a.bankAccountId)} size="sm">
                {a.name}
              </Anchor>
              {": "}
              {t("bankOpenLines", { count: a.openLines })}
              {a.difference !== null &&
                Number(a.difference) !== 0 &&
                ` · ${t("bankDifference")} ${money(a.difference)}`}
            </List.Item>
          ))}
      </List>
    </Alert>
  );
};
