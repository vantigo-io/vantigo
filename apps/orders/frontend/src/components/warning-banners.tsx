import { Alert, Button, Group, Stack, Text } from "@mantine/core";
import { IconAlertOctagon, IconInfoCircle, IconShieldCheck } from "@tabler/icons-react";
import { useState } from "react";
import type { CustomerSnapshot, Warning } from "../api/types";
import type { OrdersAccess } from "../lib/access";
import { useOrdersFormat } from "../lib/format";
import { ReasonDialog } from "./reason-dialog";
import { WarningDialog } from "./warning-dialog";

export interface WarningBannersProps {
  warnings: readonly Warning[];
  customer: CustomerSnapshot;
  access: OrdersAccess;
  /** Whether the document is at a step a block stops: a draft to send or confirm. */
  blocking: boolean;
  /** The reason a block is overridden with, once a settings holder gave one. */
  override: string | null;
  onOverride: (reason: string | null) => void;
}

/**
 * The sales warnings of a quote or an order (O33): its customer's and its
 * products', an info warning in yellow and a block in red. A block stops send
 * and confirm; a holder of `orders:settings` may override it with a reason,
 * which the next send or confirm carries and the change log keeps, and may set
 * the customer's warning from here.
 */
export const WarningBanners = ({ warnings, customer, access, blocking, override, onOverride }: WarningBannersProps) => {
  const { t, money } = useOrdersFormat();
  const [dialog, setDialog] = useState<"override" | "customer" | null>(null);
  // The credit warning (invoices phase 6 design I38) is computed, never
  // stored: the server answers it at info on a draft, and the confirm judges
  // it a block — so at the step it stops, it is shown and overridden as one.
  const levelOf = (w: Warning) =>
    w.scope === "credit" && w.code === "credit_limit_exceeded" && blocking ? "block" : w.level;
  const blocks = warnings.some((w) => levelOf(w) === "block");
  const creditBlocks = warnings.some((w) => w.scope === "credit" && levelOf(w) === "block");
  const customerWarning = warnings.find((w) => w.scope === "customer");
  const subject = (w: Warning) =>
    t(w.scope === "customer" ? "warningOnCustomer" : w.scope === "credit" ? "warningOnCredit" : "warningOnProduct", {
      name: w.name,
    });
  // A computed warning is worded here from its figures; a stored one is the
  // text a person wrote.
  const textOf = (w: Warning) => {
    if (w.scope !== "credit") return w.text;
    if (w.code === "credit_not_checked" || !w.credit) return t("creditNotChecked");
    const parts = [
      t("creditLimitExceededText", {
        exposure: money(Number(w.credit.exposureNok), "NOK"),
        limit: money(Number(w.credit.limit), "NOK"),
      }),
    ];
    if (w.credit.uncounted.length > 0) {
      parts.push(
        t("creditUncounted", {
          amounts: w.credit.uncounted.map((u) => money(Number(u.amount), u.currency)).join(", "),
        }),
      );
    }
    if (!w.credit.invoicesCounted) parts.push(t("creditInvoicesNotCounted"));
    return parts.join(" ");
  };
  if (warnings.length === 0 && !access.canManageSettings) return null;
  return (
    <Stack gap="xs" data-testid="warning-banners">
      {warnings.map((w) => (
        <Alert
          key={`${w.scope}-${w.id}`}
          color={levelOf(w) === "block" ? "red" : "yellow"}
          icon={levelOf(w) === "block" ? <IconAlertOctagon size={16} /> : <IconInfoCircle size={16} />}
          title={subject(w)}
          data-level={levelOf(w)}
        >
          <Text size="sm" style={{ whiteSpace: "pre-wrap" }}>
            {textOf(w)}
          </Text>
          {levelOf(w) === "block" && blocking && !override && (
            <Text size="xs" c="dimmed" mt={4}>
              {access.canManageSettings ? t("warningBlocksOverride") : t("warningBlocks")}
            </Text>
          )}
        </Alert>
      ))}
      {blocks && blocking && override && (
        <Alert color="gray" icon={<IconShieldCheck size={16} />} title={t("warningOverridden")}>
          <Group justify="space-between">
            <Text size="sm">{override}</Text>
            <Button size="xs" variant="subtle" onClick={() => onOverride(null)}>
              {t("undoOverride")}
            </Button>
          </Group>
        </Alert>
      )}
      {access.canManageSettings && (
        <Group gap="xs" data-assist="orders.document.warnings">
          {blocks && blocking && !override && creditBlocks && (
            <Button
              size="xs"
              color="red"
              variant="light"
              onClick={() => setDialog("override")}
              data-assist="orders.confirm.credit-override"
            >
              {t("overrideWarning")}
            </Button>
          )}
          {blocks && blocking && !override && !creditBlocks && (
            <Button size="xs" color="red" variant="light" onClick={() => setDialog("override")}>
              {t("overrideWarning")}
            </Button>
          )}
          <Button size="xs" variant="subtle" color="gray" onClick={() => setDialog("customer")}>
            {customerWarning ? t("editCustomerWarning") : t("addCustomerWarning")}
          </Button>
        </Group>
      )}
      {dialog === "override" && (
        <ReasonDialog
          title={t("overrideWarning")}
          description={t("overrideHint")}
          label={t("overrideReason")}
          submitLabel={t("overrideWarning")}
          required
          danger
          maxLength={500}
          onSubmit={(reason) => {
            onOverride(reason);
            setDialog(null);
          }}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog === "customer" && (
        <WarningDialog
          scope="customer"
          id={customer.customerId}
          subject={customer.name}
          current={customerWarning}
          onClose={() => setDialog(null)}
        />
      )}
    </Stack>
  );
};
