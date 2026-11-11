import { Alert, Badge, Button, Group, Modal, Select, Stack, Table, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPencil, IconPlus } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { bomQueryOptions } from "../api/boms";
import {
  addOrderComponent,
  isOpenOrder,
  type ManufacturingOrder,
  type ManufacturingOrderComponent,
  type ManufacturingOrderWithShortLines,
  updateOrderComponent,
} from "../api/orders";
import { isRevisionConflict, refusalCode } from "../api/request";
import type { ManufacturingVariantRef } from "../api/variants";
import { compareDecimal, isDecimal, isPositiveDecimal, normalizeDecimal } from "../lib/decimal";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";
import { VariantPicker } from "./variant-picker";

export interface OrderComponentsProps {
  order: ManufacturingOrder;
  canManage: boolean;
  /** A write that reserved answers the lines it left short; the page shows them. */
  onShortLines: (answer: ManufacturingOrderWithShortLines) => void;
}

/**
 * The order's component lines (M18, M21a): required, reserved, available at
 * the site, consumed, scrapped and what is missing, said plainly. A draft's
 * lines follow its BoM and cannot be edited; on a confirmed or in-progress
 * order a manager adds a component or changes a line's requirement.
 */
export const OrderComponents = ({ order, canManage, onShortLines }: OrderComponentsProps) => {
  const { t, quantity } = useManufacturingFormat();
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<ManufacturingOrderComponent>();
  const editable = canManage && isOpenOrder(order);

  return (
    <PageSection
      title={t("components")}
      actions={
        editable && (
          <Button
            variant="light"
            size="xs"
            leftSection={<IconPlus size={14} />}
            data-assist="manufacturing.order.add-component"
            onClick={() => setAdding(true)}
          >
            {t("addComponent")}
          </Button>
        )
      }
    >
      <Stack gap="sm" data-assist="manufacturing.order.components">
        {order.status === "draft" && (
          <Text size="sm" c="dimmed">
            {t("draftComponentsHint")}
          </Text>
        )}
        {order.components.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("noComponents")}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={820}>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("component")}</Table.Th>
                  <Table.Th>{t("requiredColumn")}</Table.Th>
                  <Table.Th>{t("reservedColumn")}</Table.Th>
                  <Table.Th>{t("availableColumn")}</Table.Th>
                  <Table.Th>{t("consumedColumn")}</Table.Th>
                  <Table.Th>{t("scrappedColumn")}</Table.Th>
                  <Table.Th>{t("missingColumn")}</Table.Th>
                  {editable && <Table.Th />}
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {order.components.map((line, index) => {
                  const unit = line.variant.unit;
                  const missing = compareDecimal(line.missing, "0") > 0;
                  return (
                    <Table.Tr key={line.id ?? `bom-${index}`}>
                      <Table.Td>
                        <Group gap={4}>
                          <Text size="sm">{line.variant.sku}</Text>
                          {line.added && (
                            <Badge size="xs" variant="light" color="grape">
                              {t("addedComponent")}
                            </Badge>
                          )}
                        </Group>
                        <Text size="xs" c="dimmed">
                          {line.variant.productName}
                        </Text>
                      </Table.Td>
                      <Table.Td>{quantity(line.required, unit)}</Table.Td>
                      <Table.Td>{quantity(line.reserved, unit)}</Table.Td>
                      <Table.Td>{quantity(line.available, unit)}</Table.Td>
                      <Table.Td>{quantity(line.consumed, unit)}</Table.Td>
                      <Table.Td>{quantity(line.scrapped, unit)}</Table.Td>
                      <Table.Td>
                        {missing ? (
                          <Text size="sm" c="red" fw={500}>
                            {t("missingValue", { quantity: quantity(line.missing, unit) })}
                          </Text>
                        ) : (
                          <Text size="sm" c="teal">
                            {t("covered")}
                          </Text>
                        )}
                      </Table.Td>
                      {editable && (
                        <Table.Td>
                          {line.id !== null && (
                            <Button
                              variant="subtle"
                              size="xs"
                              leftSection={<IconPencil size={14} />}
                              aria-label={t("editRequiredOf", { sku: line.variant.sku })}
                              onClick={() => setEditing(line)}
                            >
                              {t("editRequired")}
                            </Button>
                          )}
                        </Table.Td>
                      )}
                    </Table.Tr>
                  );
                })}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
      <Modal opened={adding} onClose={() => setAdding(false)} title={t("addComponent")}>
        {adding && <AddComponentForm order={order} onDone={onShortLines} onClose={() => setAdding(false)} />}
      </Modal>
      <Modal
        opened={editing !== undefined}
        onClose={() => setEditing(undefined)}
        title={editing ? t("editRequiredOf", { sku: editing.variant.sku }) : ""}
      >
        {editing && (
          <EditRequiredForm order={order} line={editing} onDone={onShortLines} onClose={() => setEditing(undefined)} />
        )}
      </Modal>
    </PageSection>
  );
};

/** Sends a line's write, says it, and hands the answer's short lines to the page. */
const useLineWrite = (onDone: (answer: ManufacturingOrderWithShortLines) => void, onClose: () => void) => {
  const { t } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  const [problem, setProblem] = useState<string>();
  const [code, setCode] = useState<string>();
  const [pending, setPending] = useState(false);
  const run = async (work: () => Promise<ManufacturingOrderWithShortLines>, done: string) => {
    setPending(true);
    setProblem(undefined);
    setCode(undefined);
    try {
      const answer = await work();
      notifications.show({ color: "teal", title: done, message: answer.order.number });
      onDone(answer);
      await invalidate();
      onClose();
    } catch (error) {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await invalidate();
        onClose();
        return;
      }
      setCode(refusalCode(error));
      setProblem(refusalMessage(t, error as Error));
    } finally {
      setPending(false);
    }
  };
  return { run, problem, code, pending };
};

const requiredProblem = (text: string, allowZero: boolean): string | undefined => {
  if (!text.trim()) return "required";
  if (!isDecimal(text)) return "quantity";
  if (!allowZero && !isPositiveDecimal(text)) return "positive";
  return undefined;
};

const AddComponentForm = ({
  order,
  onDone,
  onClose,
}: {
  order: ManufacturingOrder;
  onDone: (answer: ManufacturingOrderWithShortLines) => void;
  onClose: () => void;
}) => {
  const { t } = useManufacturingFormat();
  const bom = useQuery(bomQueryOptions(order.bomId));
  const [variant, setVariant] = useState<ManufacturingVariantRef>();
  const [required, setRequired] = useState("");
  const [operationId, setOperationId] = useState<string | null>(null);
  const [note, setNote] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, problem, pending } = useLineWrite(onDone, onClose);

  const submit = () => {
    const found: Record<string, string> = {};
    if (!variant) found.variant = t("problem_required");
    const requiredIssue = requiredProblem(required, false);
    if (requiredIssue) found.required = t(`problem_${requiredIssue}`);
    setErrors(found);
    if (Object.keys(found).length > 0 || !variant) return;
    void run(
      () =>
        addOrderComponent(order.id, {
          revision: order.revision,
          variantId: variant.id,
          required: normalizeDecimal(required),
          operationId: operationId === null ? null : Number(operationId),
          ...(note.trim() ? { note: note.trim() } : {}),
        }),
      t("componentAdded"),
    );
  };

  return (
    <Stack gap="md">
      <VariantPicker label={t("component")} withAsterisk value={variant} onChange={setVariant} error={errors.variant} />
      <TextInput
        label={t("required")}
        withAsterisk
        inputMode="decimal"
        value={required}
        onChange={(event) => setRequired(event.currentTarget.value)}
        error={errors.required}
      />
      <Select
        label={t("consumedBy")}
        placeholder={t("noOperation")}
        clearable
        data={(bom.data?.operations ?? []).map((operation) => ({
          value: String(operation.id),
          label: `${operation.sequence} · ${operation.name}`,
        }))}
        value={operationId}
        onChange={setOperationId}
      />
      <TextInput label={t("note")} value={note} onChange={(event) => setNote(event.currentTarget.value)} />
      {problem && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
          {problem}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button variant="default" onClick={onClose}>
          {t("cancel")}
        </Button>
        <Button loading={pending} onClick={submit}>
          {t("addComponent")}
        </Button>
      </Group>
    </Stack>
  );
};

const EditRequiredForm = ({
  order,
  line,
  onDone,
  onClose,
}: {
  order: ManufacturingOrder;
  line: ManufacturingOrderComponent;
  onDone: (answer: ManufacturingOrderWithShortLines) => void;
  onClose: () => void;
}) => {
  const { t, quantity } = useManufacturingFormat();
  const [required, setRequired] = useState(line.required);
  const [note, setNote] = useState(line.note);
  const [error, setError] = useState<string>();
  const { run, problem, code, pending } = useLineWrite(onDone, onClose);
  const unit = line.variant.unit;

  const submit = () => {
    const issue = requiredProblem(required, true);
    setError(issue ? t(`problem_${issue}`) : undefined);
    if (issue || line.id === null) return;
    const componentId = line.id;
    void run(
      () =>
        updateOrderComponent(order.id, componentId, {
          revision: order.revision,
          required: normalizeDecimal(required),
          note: note.trim(),
        }),
      t("componentSaved"),
    );
  };

  return (
    <Stack gap="md">
      <TextInput
        label={t("required")}
        description={t("requiredUsedHint", {
          consumed: quantity(line.consumed, unit),
          scrapped: quantity(line.scrapped, unit),
        })}
        withAsterisk
        inputMode="decimal"
        value={required}
        onChange={(event) => setRequired(event.currentTarget.value)}
        error={error ?? (code === "required_below_used" ? t("refusal.required_below_used") : undefined)}
      />
      <TextInput label={t("note")} value={note} onChange={(event) => setNote(event.currentTarget.value)} />
      {problem && code !== "required_below_used" && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
          {problem}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button variant="default" onClick={onClose}>
          {t("cancel")}
        </Button>
        <Button loading={pending} onClick={submit}>
          {t("save")}
        </Button>
      </Group>
    </Stack>
  );
};
