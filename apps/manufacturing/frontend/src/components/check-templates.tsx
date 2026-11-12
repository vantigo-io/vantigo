import { ActionIcon, Button, Checkbox, Group, Paper, Select, SimpleGrid, Stack, Text, TextInput } from "@mantine/core";
import { IconPlus, IconTrash } from "@tabler/icons-react";
import { checkFrequencies, checkKinds, MAX_CHECKS_PER_OPERATION } from "../api/quality";
import { type EditorCheck, newCheck } from "../lib/bom-body";
import { useManufacturingFormat } from "../lib/format";

export interface CheckTemplatesProps {
  /** The operation's row label, for the fields' accessible names. */
  row: string;
  /** The operation's path in the request (`operations[2]`), which a field error is keyed by. */
  at: string;
  checks: readonly EditorCheck[];
  readOnly: boolean;
  errors: Record<string, string>;
  onChange: (checks: EditorCheck[]) => void;
}

/**
 * An operation's quality checks in the BoM editor (manufacturing phase 2
 * M95): each a kind — instruction, pass/fail, measure, photo — a title, when
 * it is recorded (each work order, or the first piece) and, for a measure,
 * its nominal, limits and unit; quarantine on fail puts the order's output
 * on hold when it fails. At most 20 an operation, saved with the BoM.
 */
export const CheckTemplates = ({ row, at, checks, readOnly, errors, onChange }: CheckTemplatesProps) => {
  const { t } = useManufacturingFormat();
  const set = (rowKey: string, patch: Partial<EditorCheck>) =>
    onChange(checks.map((check) => (check.rowKey === rowKey ? { ...check, ...patch } : check)));

  return (
    <Stack gap="xs" data-assist="manufacturing.bom.checks">
      <Group justify="space-between">
        <Text size="sm" fw={500}>
          {t("qualityChecks")}
        </Text>
        {!readOnly && (
          <Button
            size="xs"
            variant="subtle"
            leftSection={<IconPlus size={14} />}
            disabled={checks.length >= MAX_CHECKS_PER_OPERATION}
            onClick={() => onChange([...checks, newCheck()])}
          >
            {t("addCheck")}
          </Button>
        )}
      </Group>
      {checks.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("noChecks")}
        </Text>
      )}
      {checks.map((check, index) => {
        const path = `${at}.checks[${index}]`;
        const label = `${row}, ${t("checkRow", { number: index + 1 })}`;
        return (
          <Paper key={check.rowKey} withBorder p="xs">
            <Stack gap="xs">
              <Group align="flex-start" wrap="nowrap">
                <SimpleGrid cols={{ base: 1, sm: 3 }} style={{ flex: 1 }}>
                  <TextInput
                    aria-label={`${label}: ${t("checkTitle")}`}
                    placeholder={t("checkTitle")}
                    maxLength={200}
                    disabled={readOnly}
                    value={check.title}
                    onChange={(event) => set(check.rowKey, { title: event.currentTarget.value })}
                    error={errors[`${path}.title`]}
                  />
                  <Select
                    aria-label={`${label}: ${t("checkKind")}`}
                    data={checkKinds.map((kind) => ({ value: kind, label: t(`checkKind_${kind}`) }))}
                    allowDeselect={false}
                    disabled={readOnly}
                    value={check.kind}
                    onChange={(next) => next && set(check.rowKey, { kind: next as EditorCheck["kind"] })}
                    error={errors[`${path}.kind`]}
                  />
                  <Select
                    aria-label={`${label}: ${t("checkFrequency")}`}
                    data={checkFrequencies.map((frequency) => ({
                      value: frequency,
                      label: t(`checkFrequency_${frequency}`),
                    }))}
                    allowDeselect={false}
                    disabled={readOnly}
                    value={check.frequency}
                    onChange={(next) => next && set(check.rowKey, { frequency: next as EditorCheck["frequency"] })}
                    error={errors[`${path}.frequency`]}
                  />
                </SimpleGrid>
                {!readOnly && (
                  <ActionIcon
                    variant="subtle"
                    color="red"
                    aria-label={`${label}: ${t("removeCheck")}`}
                    onClick={() => onChange(checks.filter((other) => other.rowKey !== check.rowKey))}
                  >
                    <IconTrash size={16} />
                  </ActionIcon>
                )}
              </Group>
              {check.kind === "measure" && (
                <SimpleGrid cols={{ base: 2, sm: 4 }}>
                  {(["nominal", "min", "max"] as const).map((field) => (
                    <TextInput
                      key={field}
                      label={t(`check_${field}`)}
                      inputMode="decimal"
                      disabled={readOnly}
                      value={check[field]}
                      onChange={(event) => set(check.rowKey, { [field]: event.currentTarget.value })}
                      error={errors[`${path}.${field}`]}
                    />
                  ))}
                  <TextInput
                    label={t("check_unit")}
                    maxLength={20}
                    disabled={readOnly}
                    value={check.unit}
                    onChange={(event) => set(check.rowKey, { unit: event.currentTarget.value })}
                    error={errors[`${path}.unit`]}
                  />
                </SimpleGrid>
              )}
              <TextInput
                aria-label={`${label}: ${t("checkInstructions")}`}
                placeholder={t("checkInstructions")}
                maxLength={5000}
                disabled={readOnly}
                value={check.instructions}
                onChange={(event) => set(check.rowKey, { instructions: event.currentTarget.value })}
                error={errors[`${path}.instructions`]}
              />
              <Checkbox
                label={t("quarantineOnFail")}
                description={t("quarantineOnFailHint")}
                disabled={readOnly}
                checked={check.quarantineOnFail}
                onChange={(event) => set(check.rowKey, { quarantineOnFail: event.currentTarget.checked })}
              />
            </Stack>
          </Paper>
        );
      })}
    </Stack>
  );
};
