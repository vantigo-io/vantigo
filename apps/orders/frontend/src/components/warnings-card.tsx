import { Badge, Button, Group, Stack, Table, Text, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconPencil, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { ORDERS_QUERY_KEY } from "../api/request";
import type { Warning, WarningScope } from "../api/types";
import { deleteWarning, warningsQueryOptions } from "../api/warnings";
import type { OrdersAccess } from "../lib/access";
import { refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";
import { CustomerPicker } from "./customer-picker";
import { VariantPicker } from "./variant-picker";
import { WarningDialog } from "./warning-dialog";

interface Editing {
  scope: WarningScope;
  id: number;
  subject: string;
  current?: Warning;
}

/**
 * The sales warnings (O33) as Orders' settings list them: every customer's
 * and product's, each with its level and text, edited or removed in place;
 * a new one is set on a customer found with the customer picker or a product
 * found through one of its variants. A product warning whose product is
 * deleted is marked so and can only be removed. `orders:settings` is the
 * page's key.
 */
export const WarningsCard = ({ access, currency }: { access: OrdersAccess; currency: string }) => {
  const { t, dateTime } = useOrdersFormat();
  const warnings = useQuery(warningsQueryOptions());
  const [editing, setEditing] = useState<Editing | null>(null);
  const [adding, setAdding] = useState<WarningScope | null>(null);
  const [customerId, setCustomerId] = useState<number | null>(null);
  const queryClient = useQueryClient();
  const removeOrphan = useMutation({
    mutationFn: (id: number) => deleteWarning("product", id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ORDERS_QUERY_KEY] });
      notifications.show({ color: "green", message: t("warningRemoved") });
    },
    onError: (e) => notifications.show({ color: "red", message: refusalMessage(e, t) }),
  });
  return (
    <PageSection data-assist="orders.settings.warnings">
      <Stack gap="md">
        <div>
          <Title order={4}>{t("warningsTitle")}</Title>
          <Text size="sm" c="dimmed">
            {t("warningsDescription")}
          </Text>
        </div>
        {warnings.isError && <Text c="red">{refusalMessage(warnings.error, t)}</Text>}
        {warnings.data && warnings.data.items.length === 0 && (
          <Text size="sm" c="dimmed">
            {t("noWarnings")}
          </Text>
        )}
        {warnings.data && warnings.data.items.length > 0 && (
          <Table data-testid="warnings-table">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("warningSubject")}</Table.Th>
                <Table.Th>{t("warningLevel")}</Table.Th>
                <Table.Th>{t("warningText")}</Table.Th>
                <Table.Th>{t("warningUpdated")}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {warnings.data.items.map((w) => (
                <Table.Tr key={`${w.scope}-${w.id}`}>
                  <Table.Td>
                    <Text size="xs" c="dimmed">
                      {t(w.scope === "customer" ? "customerWarning" : "productWarning")}
                    </Text>
                    <Text size="sm">{w.name || `#${w.id}`}</Text>
                    {w.orphaned && (
                      <Badge color="gray" variant="light" size="sm">
                        {t("productDeleted")}
                      </Badge>
                    )}
                  </Table.Td>
                  <Table.Td>
                    <Badge color={w.level === "block" ? "red" : "yellow"} variant="light">
                      {t(`warningLevel.${w.level}`)}
                    </Badge>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm" style={{ whiteSpace: "pre-wrap" }}>
                      {w.text}
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    <Text size="xs" c="dimmed">
                      {dateTime(w.updatedAt)}
                      {w.updatedBy ? ` · ${w.updatedBy.displayName}` : ""}
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    {w.orphaned ? (
                      <Button
                        size="xs"
                        variant="subtle"
                        color="red"
                        leftSection={<IconTrash size={14} />}
                        loading={removeOrphan.isPending && removeOrphan.variables === w.id}
                        onClick={() => removeOrphan.mutate(w.id)}
                      >
                        {t("removeOrphanWarning")}
                      </Button>
                    ) : (
                      <Button
                        size="xs"
                        variant="subtle"
                        leftSection={<IconPencil size={14} />}
                        onClick={() => setEditing({ scope: w.scope, id: w.id, subject: w.name, current: w })}
                      >
                        {t("editWarning")}
                      </Button>
                    )}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
        <Group gap="xs" align="flex-end">
          {access.canViewCustomers && (
            <Button size="xs" variant="light" onClick={() => setAdding("customer")}>
              {t("addCustomerWarning")}
            </Button>
          )}
          <Button size="xs" variant="light" onClick={() => setAdding("product")}>
            {t("addProductWarning")}
          </Button>
        </Group>
        {adding === "customer" && (
          <CustomerPicker
            label={t("warningCustomer")}
            value={customerId}
            onChange={setCustomerId}
            onPicked={(c) => {
              if (!c) return;
              setAdding(null);
              setCustomerId(null);
              const current = warnings.data?.items.find((w) => w.scope === "customer" && w.id === c.id);
              setEditing({ scope: "customer", id: c.id, subject: c.name, current });
            }}
          />
        )}
        {adding === "product" && (
          <VariantPicker
            label={t("warningProduct")}
            currency={currency}
            value={null}
            onChange={(v) => {
              if (!v) return;
              setAdding(null);
              const current = warnings.data?.items.find((w) => w.scope === "product" && w.id === v.productId);
              setEditing({ scope: "product", id: v.productId, subject: v.name, current });
            }}
          />
        )}
      </Stack>
      {editing && (
        <WarningDialog
          scope={editing.scope}
          id={editing.id}
          subject={editing.subject}
          current={editing.current}
          onClose={() => setEditing(null)}
        />
      )}
    </PageSection>
  );
};
