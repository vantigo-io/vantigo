import { Alert, Button, Group, Modal, Stack, Table, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useState } from "react";
import type { ManufacturingOrderComponent } from "../api/orders";
import { reportScrap } from "../api/shop-floor";
import { isDecimal, isPositiveDecimal, normalizeDecimal } from "../lib/decimal";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";

export interface ScrapDialogProps {
  workOrderId: number;
  orderNumber: string;
  unit: string;
  /** The order's component lines consumed at this operation. */
  components: readonly ManufacturingOrderComponent[];
  opened: boolean;
  onClose: () => void;
}

/** A field left empty is no scrap; anything else must be a decimal. */
const fieldProblem = (text: string) => text.trim() !== "" && !isDecimal(text);

/**
 * Report scrap at a work order (M31): each component's scrap and the finished
 * units discarded, posted as a production with no good output. One
 * `clientRequestId` per opening makes a retry the same report.
 */
export const ScrapDialog = ({ opened, onClose, ...props }: ScrapDialogProps) => {
  const { t } = useManufacturingFormat();
  return (
    <Modal opened={opened} onClose={onClose} title={t("reportScrapTitle", { number: props.orderNumber })} size="lg">
      {opened && <ScrapForm {...props} onClose={onClose} />}
    </Modal>
  );
};

const ScrapForm = ({ workOrderId, unit, components, onClose }: Omit<ScrapDialogProps, "opened" | "orderNumber">) => {
  const { t } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  const [clientRequestId] = useState(() => crypto.randomUUID());
  const [scrap, setScrap] = useState<Record<number, string>>({});
  const [outputScrap, setOutputScrap] = useState("");
  const [problem, setProblem] = useState<string>();
  const [sending, setSending] = useState(false);

  const lines = components.flatMap((line) => {
    const text = scrap[line.id ?? 0] ?? "";
    return line.id !== null && line.id !== undefined && isPositiveDecimal(text)
      ? [{ componentId: line.id, scrap: normalizeDecimal(text) }]
      : [];
  });
  const output = isPositiveDecimal(outputScrap) ? normalizeDecimal(outputScrap) : undefined;
  const invalid =
    fieldProblem(outputScrap) ||
    Object.values(scrap).some(fieldProblem) ||
    (lines.length === 0 && output === undefined);

  const send = async () => {
    setSending(true);
    setProblem(undefined);
    try {
      await reportScrap(workOrderId, { clientRequestId, lines, ...(output ? { outputScrap: output } : {}) });
      notifications.show({ color: "teal", title: t("scrapReported"), message: "" });
      await invalidate();
      onClose();
    } catch (error) {
      setProblem(refusalMessage(t, error as Error));
    } finally {
      setSending(false);
    }
  };

  return (
    <Stack gap="md">
      <TextInput
        size="lg"
        label={t("outputScrap")}
        description={t("outputScrapHint")}
        inputMode="decimal"
        rightSection={<Text size="sm">{unit}</Text>}
        value={outputScrap}
        onChange={(event) => setOutputScrap(event.currentTarget.value)}
        error={fieldProblem(outputScrap) ? t("problem_notNegative") : undefined}
      />
      {components.length > 0 && (
        <Table>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("component")}</Table.Th>
              <Table.Th>{t("scrapColumn")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {components.map((line) => {
              const id = line.id ?? 0;
              const text = scrap[id] ?? "";
              return (
                <Table.Tr key={id}>
                  <Table.Td>
                    <Text size="md">{line.variant.sku}</Text>
                    <Text size="sm" c="dimmed">
                      {line.variant.productName}
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    <TextInput
                      size="lg"
                      aria-label={t("scrapOf", { sku: line.variant.sku })}
                      inputMode="decimal"
                      placeholder={t("noScrap")}
                      rightSection={<Text size="sm">{line.variant.unit}</Text>}
                      value={text}
                      onChange={(event) => {
                        const value = event.currentTarget.value;
                        setScrap((current) => ({ ...current, [id]: value }));
                      }}
                      error={fieldProblem(text)}
                    />
                  </Table.Td>
                </Table.Tr>
              );
            })}
          </Table.Tbody>
        </Table>
      )}
      {problem && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotReportScrap")}>
          {problem}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button size="lg" variant="default" onClick={onClose}>
          {t("cancel")}
        </Button>
        <Button size="lg" loading={sending} disabled={invalid} onClick={() => void send()}>
          {t("reportScrap")}
        </Button>
      </Group>
    </Stack>
  );
};
