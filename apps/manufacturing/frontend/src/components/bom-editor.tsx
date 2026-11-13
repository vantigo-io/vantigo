import {
  ActionIcon,
  Alert,
  Button,
  Checkbox,
  Group,
  List,
  MultiSelect,
  NumberInput,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Table,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { createBom, MAX_BOM_LINES, MAX_BOM_OPERATIONS, type ManufacturingBom, updateBom } from "../api/boms";
import { manufacturingMetaQueryOptions } from "../api/meta";
import { isRevisionConflict, refusalCode, refusalCodes, refusedFields, refusedLines } from "../api/request";
import { workCenterGroupsQueryOptions, workCentersQueryOptions } from "../api/work-centers";
import {
  type BomEditorState,
  bomCreateBody,
  bomUpdateBody,
  type EditorLine,
  type EditorOperation,
  editorOf,
  editorProblems,
  emptyEditor,
  newLine,
  newOperation,
  operationLevels,
  removeOperation,
} from "../lib/bom-body";
import { canManageBoms } from "../lib/capabilities";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";
import { LOT_NUMBER, renderLotPattern, sampleLotNumber } from "../lib/lots";
import { BOMS_ROUTE_PATH, bomLinkOptions } from "../lib/routes";
import { CheckTemplates } from "./check-templates";
import { VariantPicker } from "./variant-picker";

/** Which header field a refusal code belongs to, when it is one field's. */
const codeField: Record<string, string> = {
  bom_code_taken: "code",
  variant_not_goods: "variantId",
  variant_not_found: "variantId",
};

export interface BomEditorProps {
  /** The BoM to edit; a new one when absent. */
  bom?: ManufacturingBom;
}

/**
 * A BoM's editor (M10–M13): the variant it makes — fixed once saved — its
 * code, output quantity, make-to-order, lead time and note; its operations
 * (sequence, name, active work center, setup, minutes per cycle, Markdown
 * instructions); and its component lines (variant, quantity, scrap, the
 * operation that consumes it, sort order, note). Everything is saved in one
 * request; lines name operations by client key (`lib/bom-body`). The
 * server's refusals are marked where they belong: a field error on its
 * field, a line's code on its row, a cycle with the SKUs it goes through.
 * Read only without `manufacturing:boms-manage` or once archived.
 */
export const BomEditor = ({ bom }: BomEditorProps) => {
  const { t } = useManufacturingFormat();
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const invalidate = useInvalidateManufacturing();
  const meta = useQuery(manufacturingMetaQueryOptions());
  const centers = useQuery(workCentersQueryOptions({ status: "active" }));
  const groups = useQuery(workCenterGroupsQueryOptions({ status: "active" }));
  const [state, setState] = useState<BomEditorState>(() => (bom ? editorOf(bom) : emptyEditor()));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [lineCodes, setLineCodes] = useState<Record<number, string>>({});
  const [problem, setProblem] = useState<string>();
  const readOnly = !canManageBoms(meta.data) || bom?.status === "archived";

  const setHeader = (patch: Partial<BomEditorState["header"]>) =>
    setState((current) => ({ ...current, header: { ...current.header, ...patch } }));
  const setOperation = (key: string, patch: Partial<EditorOperation>) =>
    setState((current) => ({
      ...current,
      operations: current.operations.map((operation) =>
        operation.key === key ? { ...operation, ...patch } : operation,
      ),
    }));
  const setLine = (rowKey: string, patch: Partial<EditorLine>) =>
    setState((current) => ({
      ...current,
      lines: current.lines.map((line) => (line.rowKey === rowKey ? { ...line, ...patch } : line)),
    }));

  const save = useMutation({
    mutationFn: () => (bom ? updateBom(bom.id, bomUpdateBody(state, bom.revision)) : createBom(bomCreateBody(state))),
    onSuccess: async (saved) => {
      notifications.show({ color: "teal", title: t(bom ? "bomSaved" : "bomCreated"), message: saved.variant.sku });
      await invalidate();
      if (!bom) void navigate({ ...bomLinkOptions(saved.id), replace: true });
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await invalidate();
        return;
      }
      const shown: Record<string, string> = { ...refusedFields(error) };
      const code = refusalCode(error);
      if (code && codeField[code] && refusedLines(error).length === 0) shown[codeField[code]] = t(`refusal.${code}`);
      setErrors(shown);
      setLineCodes(
        Object.fromEntries(
          refusedLines(error).map((line) => [
            line.index,
            (refusalCodes as readonly string[]).includes(line.code) ? t(`refusal.${line.code}`) : line.detail,
          ]),
        ),
      );
      setProblem(refusalMessage(t, error));
    },
  });

  const submit = () => {
    const found = Object.fromEntries(
      Object.entries(editorProblems(state)).map(([field, key]) => [field, t(`problem_${key}`)]),
    );
    setErrors(found);
    setLineCodes({});
    setProblem(Object.keys(found).length > 0 ? t("fixMarkedFields") : undefined);
    if (Object.keys(found).length === 0) save.mutate();
  };

  // An operation targets a work center or a group of them (manufacturing phase 2 M118): one select, keyed c:/g:.
  const centerOptions = (centers.data ?? []).map((center) => ({
    value: `c:${center.id}`,
    label: `${center.code} · ${center.name} (${center.siteCode})`,
  }));
  for (const group of groups.data ?? [])
    centerOptions.push({ value: `g:${group.id}`, label: t("groupOption", { code: group.code, name: group.name }) });
  // A saved operation keeps naming its work center even once it is archived, so the row says which.
  for (const operation of bom?.operations ?? []) {
    const value =
      operation.workCenterGroupId === null ? `c:${operation.workCenterId}` : `g:${operation.workCenterGroupId}`;
    if (!centerOptions.some((option) => option.value === value))
      centerOptions.push({ value, label: `${operation.workCenterCode} · ${operation.workCenterName}` });
  }
  const targetOf = (operation: EditorOperation) =>
    operation.workCenterGroupId !== null
      ? `g:${operation.workCenterGroupId}`
      : operation.workCenterId === null
        ? null
        : `c:${operation.workCenterId}`;
  const targetPatch = (value: string | null): Partial<EditorOperation> =>
    value === null
      ? { workCenterId: null, workCenterGroupId: null }
      : value.startsWith("g:")
        ? { workCenterId: null, workCenterGroupId: Number(value.slice(2)) }
        : { workCenterId: Number(value.slice(2)), workCenterGroupId: null };
  const levels = operationLevels(state.operations);
  const operationOptions = state.operations.map((operation, index) => {
    const sequence = operation.sequence === "" ? (index + 1) * 10 : operation.sequence;
    return { value: operation.key, sequence, label: `${sequence} · ${operation.name || t("unnamedOperation")}` };
  });

  return (
    <Stack gap="lg">
      <PageSection title={t("bomDetails")}>
        <Stack gap="md" data-assist="manufacturing.bom.header">
          <SimpleGrid cols={{ base: 1, sm: 2, md: 3 }}>
            <VariantPicker
              label={t("productVariant")}
              withAsterisk
              value={state.header.variant}
              disabled={!!bom || readOnly}
              onChange={(variant) => setHeader({ variant })}
              error={errors.variantId}
            />
            <TextInput
              label={t("bomCode")}
              description={t("bomCodeHint")}
              maxLength={32}
              disabled={readOnly}
              value={state.header.code}
              onChange={(event) => setHeader({ code: event.currentTarget.value })}
              error={errors.code}
            />
            <TextInput
              label={t("outputQuantity")}
              description={t("outputQuantityHint")}
              withAsterisk
              inputMode="decimal"
              disabled={readOnly}
              value={state.header.outputQuantity}
              rightSection={
                state.header.variant ? (
                  <Text size="xs" c="dimmed">
                    {state.header.variant.unit}
                  </Text>
                ) : undefined
              }
              onChange={(event) => setHeader({ outputQuantity: event.currentTarget.value })}
              error={errors.outputQuantity}
            />
            <NumberInput
              label={t("leadTimeDays")}
              description={t("leadTimeDaysHint")}
              min={0}
              allowDecimal={false}
              allowNegative={false}
              disabled={readOnly}
              value={state.header.leadTimeDays}
              onChange={(next) => setHeader({ leadTimeDays: typeof next === "number" ? next : "" })}
              error={errors.leadTimeDays}
            />
          </SimpleGrid>
          <Group gap="xl">
            {!bom && (
              <Checkbox
                label={t("makeDefault")}
                description={t("makeDefaultHint")}
                disabled={readOnly}
                checked={state.header.isDefault}
                onChange={(event) => setHeader({ isDefault: event.currentTarget.checked })}
              />
            )}
            <Switch
              label={t("makeToOrder")}
              description={t("makeToOrderHint")}
              disabled={readOnly}
              checked={state.header.makeToOrder}
              onChange={(event) => setHeader({ makeToOrder: event.currentTarget.checked })}
            />
          </Group>
          <LotPatternInput
            value={state.header.lotPattern}
            sku={state.header.variant?.sku ?? ""}
            disabled={readOnly}
            error={errors.lotPattern}
            onChange={(lotPattern) => setHeader({ lotPattern })}
          />
          <Textarea
            label={t("note")}
            maxLength={2000}
            rows={2}
            disabled={readOnly}
            value={state.header.note}
            onChange={(event) => setHeader({ note: event.currentTarget.value })}
            error={errors.note}
          />
        </Stack>
      </PageSection>

      <PageSection
        title={t("operations")}
        actions={
          !readOnly && (
            <Button
              variant="light"
              size="xs"
              leftSection={<IconPlus size={14} />}
              disabled={state.operations.length >= MAX_BOM_OPERATIONS}
              onClick={() =>
                setState((current) => ({
                  ...current,
                  operations: [...current.operations, newOperation(current.operations)],
                }))
              }
            >
              {t("addOperation")}
            </Button>
          )
        }
      >
        <Stack gap="xs" data-assist="manufacturing.bom.operations">
          {state.operations.length === 0 ? (
            <Text size="sm" c="dimmed">
              {t("noOperations")}
            </Text>
          ) : (
            <Table.ScrollContainer minWidth={900}>
              <Table verticalSpacing="xs">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th w={90}>{t("sequence")}</Table.Th>
                    <Table.Th>{t("operationName")}</Table.Th>
                    <Table.Th>{t("workCenter")}</Table.Th>
                    <Table.Th w={130}>{t("setupMinutes")}</Table.Th>
                    <Table.Th w={130}>{t("minutesPerCycle")}</Table.Th>
                    {!readOnly && <Table.Th w={40} />}
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {state.operations.map((operation, index) => {
                    const at = `operations[${index}]`;
                    const row = t("operationRow", { number: index + 1 });
                    return [
                      <Table.Tr key={operation.key}>
                        <Table.Td>
                          <NumberInput
                            aria-label={`${row}: ${t("sequence")}`}
                            min={0}
                            allowDecimal={false}
                            allowNegative={false}
                            disabled={readOnly}
                            value={operation.sequence}
                            onChange={(next) =>
                              setOperation(operation.key, { sequence: typeof next === "number" ? next : "" })
                            }
                            error={errors[`${at}.sequence`]}
                          />
                        </Table.Td>
                        <Table.Td>
                          <TextInput
                            aria-label={`${row}: ${t("operationName")}`}
                            maxLength={200}
                            disabled={readOnly}
                            value={operation.name}
                            onChange={(event) => setOperation(operation.key, { name: event.currentTarget.value })}
                            error={errors[`${at}.name`]}
                          />
                        </Table.Td>
                        <Table.Td>
                          <Select
                            aria-label={`${row}: ${t("workCenter")}`}
                            searchable
                            disabled={readOnly}
                            data={centerOptions}
                            value={targetOf(operation)}
                            onChange={(next) => setOperation(operation.key, targetPatch(next))}
                            error={errors[`${at}.workCenterId`] ?? errors[`${at}.workCenterGroupId`]}
                          />
                        </Table.Td>
                        <Table.Td>
                          <TextInput
                            aria-label={`${row}: ${t("setupMinutes")}`}
                            placeholder={t("workCenterSetup")}
                            inputMode="decimal"
                            disabled={readOnly}
                            value={operation.setupMinutes}
                            onChange={(event) =>
                              setOperation(operation.key, { setupMinutes: event.currentTarget.value })
                            }
                            error={errors[`${at}.setupMinutes`]}
                          />
                        </Table.Td>
                        <Table.Td>
                          <TextInput
                            aria-label={`${row}: ${t("minutesPerCycle")}`}
                            inputMode="decimal"
                            disabled={readOnly}
                            value={operation.minutesPerCycle}
                            onChange={(event) =>
                              setOperation(operation.key, { minutesPerCycle: event.currentTarget.value })
                            }
                            error={errors[`${at}.minutesPerCycle`]}
                          />
                        </Table.Td>
                        {!readOnly && (
                          <Table.Td>
                            <ActionIcon
                              variant="subtle"
                              color="red"
                              aria-label={t("removeOperation", { number: index + 1 })}
                              onClick={() => setState((current) => removeOperation(current, operation.key))}
                            >
                              <IconTrash size={16} />
                            </ActionIcon>
                          </Table.Td>
                        )}
                      </Table.Tr>,
                      <Table.Tr key={`${operation.key}-graph`}>
                        <Table.Td />
                        <Table.Td colSpan={2}>
                          <MultiSelect
                            aria-label={`${row}: ${t("dependsOn")}`}
                            label={t("dependsOn")}
                            placeholder={operation.dependsOn.length === 0 ? t("dependsOnNone") : undefined}
                            clearable
                            disabled={readOnly}
                            // Only operations earlier by sequence (M112); one chosen before a sequence changed stays shown, marked.
                            data={operationOptions.filter(
                              (option) =>
                                option.value !== operation.key &&
                                (option.sequence < operationOptions[index].sequence ||
                                  operation.dependsOn.includes(option.value)),
                            )}
                            value={operation.dependsOn}
                            onChange={(dependsOn) => setOperation(operation.key, { dependsOn })}
                            error={errors[`${at}.dependsOn`]}
                          />
                        </Table.Td>
                        <Table.Td colSpan={readOnly ? 2 : 3}>
                          <TextInput
                            aria-label={`${row}: ${t("sendAheadQuantity")}`}
                            label={t("sendAheadQuantity")}
                            description={t("sendAheadHint")}
                            inputMode="decimal"
                            disabled={readOnly}
                            value={operation.sendAheadQuantity}
                            onChange={(event) =>
                              setOperation(operation.key, { sendAheadQuantity: event.currentTarget.value })
                            }
                            error={errors[`${at}.sendAheadQuantity`]}
                          />
                        </Table.Td>
                      </Table.Tr>,
                      <Table.Tr key={`${operation.key}-instructions`}>
                        <Table.Td />
                        <Table.Td colSpan={readOnly ? 4 : 5}>
                          <Textarea
                            aria-label={`${row}: ${t("instructions")}`}
                            placeholder={t("instructionsHint")}
                            rows={1}
                            maxLength={20_000}
                            disabled={readOnly}
                            value={operation.instructions}
                            onChange={(event) =>
                              setOperation(operation.key, { instructions: event.currentTarget.value })
                            }
                            error={errors[`${at}.instructions`]}
                          />
                        </Table.Td>
                      </Table.Tr>,
                      <Table.Tr key={`${operation.key}-checks`}>
                        <Table.Td />
                        <Table.Td colSpan={readOnly ? 4 : 5}>
                          <CheckTemplates
                            row={row}
                            at={at}
                            checks={operation.checks}
                            readOnly={readOnly}
                            errors={errors}
                            onChange={(checks) => setOperation(operation.key, { checks })}
                          />
                        </Table.Td>
                      </Table.Tr>,
                    ];
                  })}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
          {state.operations.length > 1 && (
            <Stack gap={4} data-assist="manufacturing.bom.graph">
              <Text size="sm" fw={600}>
                {t("operationGraph")}
              </Text>
              {levels === null ? (
                <Text size="sm" c="red">
                  {t("operationGraphCycle")}
                </Text>
              ) : (
                <>
                  <List size="sm" type="ordered">
                    {levels.map((level, index) => (
                      <List.Item key={level.map((operation) => operation.key).join("+")}>
                        {`${t("operationGraphLevel", { number: index + 1 })}: ${level
                          .map((operation) => operation.name || t("unnamedOperation"))
                          .join(" · ")}`}
                      </List.Item>
                    ))}
                  </List>
                  <Text size="xs" c="dimmed">
                    {t("operationGraphHint")}
                  </Text>
                </>
              )}
            </Stack>
          )}
        </Stack>
      </PageSection>

      <PageSection
        title={t("components")}
        actions={
          !readOnly && (
            <Button
              variant="light"
              size="xs"
              leftSection={<IconPlus size={14} />}
              disabled={state.lines.length >= MAX_BOM_LINES}
              onClick={() => setState((current) => ({ ...current, lines: [...current.lines, newLine(current.lines)] }))}
            >
              {t("addComponent")}
            </Button>
          )
        }
      >
        <Stack gap="xs" data-assist="manufacturing.bom.lines">
          {state.lines.length === 0 ? (
            <Text size="sm" c="dimmed">
              {t("noComponents")}
            </Text>
          ) : (
            <Table.ScrollContainer minWidth={1000}>
              <Table verticalSpacing="xs">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("component")}</Table.Th>
                    <Table.Th w={120}>{t("quantity")}</Table.Th>
                    <Table.Th w={100}>{t("scrapPercent")}</Table.Th>
                    <Table.Th w={200}>{t("consumedBy")}</Table.Th>
                    <Table.Th w={90}>{t("sortOrder")}</Table.Th>
                    <Table.Th>{t("note")}</Table.Th>
                    {!readOnly && <Table.Th w={40} />}
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {state.lines.map((line, index) => {
                    const at = `lines[${index}]`;
                    const row = t("componentRow", { number: index + 1 });
                    const code = lineCodes[index];
                    return (
                      <Table.Tr key={line.rowKey} data-refused={code ? "true" : undefined}>
                        <Table.Td>
                          <VariantPicker
                            compact
                            label={`${row}: ${t("component")}`}
                            disabled={readOnly}
                            value={line.variant}
                            onChange={(variant) => setLine(line.rowKey, { variant })}
                            error={errors[`${at}.variantId`] ?? code}
                          />
                        </Table.Td>
                        <Table.Td>
                          <TextInput
                            aria-label={`${row}: ${t("quantity")}`}
                            inputMode="decimal"
                            disabled={readOnly}
                            value={line.quantity}
                            rightSection={
                              line.variant ? (
                                <Text size="xs" c="dimmed">
                                  {line.variant.unit}
                                </Text>
                              ) : undefined
                            }
                            onChange={(event) => setLine(line.rowKey, { quantity: event.currentTarget.value })}
                            error={errors[`${at}.quantity`]}
                          />
                        </Table.Td>
                        <Table.Td>
                          <TextInput
                            aria-label={`${row}: ${t("scrapPercent")}`}
                            placeholder={t("noScrap")}
                            inputMode="decimal"
                            disabled={readOnly}
                            value={line.scrapPercent}
                            onChange={(event) => setLine(line.rowKey, { scrapPercent: event.currentTarget.value })}
                            error={errors[`${at}.scrapPercent`]}
                          />
                        </Table.Td>
                        <Table.Td>
                          <Select
                            aria-label={`${row}: ${t("consumedBy")}`}
                            placeholder={t("noOperation")}
                            clearable
                            disabled={readOnly}
                            data={operationOptions}
                            value={line.operationKey}
                            onChange={(next) => setLine(line.rowKey, { operationKey: next })}
                            error={errors[`${at}.operationKey`]}
                          />
                        </Table.Td>
                        <Table.Td>
                          <NumberInput
                            aria-label={`${row}: ${t("sortOrder")}`}
                            allowDecimal={false}
                            disabled={readOnly}
                            value={line.sortOrder}
                            onChange={(next) =>
                              setLine(line.rowKey, { sortOrder: typeof next === "number" ? next : "" })
                            }
                            error={errors[`${at}.sortOrder`]}
                          />
                        </Table.Td>
                        <Table.Td>
                          <TextInput
                            aria-label={`${row}: ${t("note")}`}
                            maxLength={2000}
                            disabled={readOnly}
                            value={line.note}
                            onChange={(event) => setLine(line.rowKey, { note: event.currentTarget.value })}
                            error={errors[`${at}.note`]}
                          />
                        </Table.Td>
                        {!readOnly && (
                          <Table.Td>
                            <ActionIcon
                              variant="subtle"
                              color="red"
                              aria-label={t("removeComponent", { number: index + 1 })}
                              onClick={() =>
                                setState((current) => ({
                                  ...current,
                                  lines: current.lines.filter((one) => one.rowKey !== line.rowKey),
                                }))
                              }
                            >
                              <IconTrash size={16} />
                            </ActionIcon>
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
      </PageSection>

      {problem && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
          {problem}
        </Alert>
      )}
      {!readOnly && (
        <Group justify="flex-end">
          <Button variant="default" onClick={() => void navigate({ to: BOMS_ROUTE_PATH })}>
            {t("cancel")}
          </Button>
          <Button data-assist="manufacturing.bom.save" loading={save.isPending} onClick={submit}>
            {t(bom ? "save" : "createBom")}
          </Button>
        </Group>
      )}
    </Stack>
  );
};

interface LotPatternInputProps {
  value: string;
  sku: string;
  disabled?: boolean;
  error?: string;
  onChange: (value: string) => void;
}

/**
 * The lot number pattern (manufacturing phase 2 M80) with a preview of the
 * number it gives today, and whether it is a lot number at all — judged, as
 * the server judges it (lot_pattern_invalid), on the widest sample: a long
 * order number and counter, not the preview's.
 */
const LotPatternInput = ({ value, sku, disabled, error, onChange }: LotPatternInputProps) => {
  const { t } = useManufacturingFormat();
  const pattern = value.trim() === "" ? "{MO}" : value.trim();
  const preview = renderLotPattern(pattern, sku);
  const widest = sampleLotNumber(pattern, sku);
  const valid = LOT_NUMBER.test(widest);
  return (
    <TextInput
      label={t("lotPattern")}
      description={`${t("lotPatternHint")} ${valid ? t("lotPatternPreview", { sample: preview }) : ""}`.trim()}
      maxLength={40}
      disabled={disabled}
      value={value}
      onChange={(event) => onChange(event.currentTarget.value)}
      error={error ?? (valid ? undefined : t("lotPatternInvalid", { sample: widest }))}
    />
  );
};
