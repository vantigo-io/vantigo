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
  const { t } = useOrdersFormat();
  const [dialog, setDialog] = useState<"override" | "customer" | null>(null);
  const blocks = warnings.some((w) => w.level === "block");
  const customerWarning = warnings.find((w) => w.scope === "customer");
  const subject = (w: Warning) =>
    t(w.scope === "customer" ? "warningOnCustomer" : "warningOnProduct", { name: w.name });
  if (warnings.length === 0 && !access.canManageSettings) return null;
  return (
    <Stack gap="xs" data-testid="warning-banners">
      {warnings.map((w) => (
        <Alert
          key={`${w.scope}-${w.id}`}
          color={w.level === "block" ? "red" : "yellow"}
          icon={w.level === "block" ? <IconAlertOctagon size={16} /> : <IconInfoCircle size={16} />}
          title={subject(w)}
          data-level={w.level}
        >
          <Text size="sm" style={{ whiteSpace: "pre-wrap" }}>
            {w.text}
          </Text>
          {w.level === "block" && blocking && !override && (
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
          {blocks && blocking && !override && (
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
