import type {
  ManufacturingBom,
  ManufacturingBomInput,
  ManufacturingBomLineInput,
  ManufacturingBomOperationInput,
  ManufacturingBomUpdate,
} from "../api/boms";
import type {
  ManufacturingCheckFrequency,
  ManufacturingCheckKind,
  ManufacturingCheckTemplateInput,
} from "../api/quality";
import type { ManufacturingVariantRef } from "../api/variants";
import { compareDecimal, isDecimal, isPositiveDecimal, normalizeDecimal } from "./decimal";

/**
 * The BoM editor's state and the request it sends (manufacturing design
 * M11–M13). Operations and lines are edited together and saved in one
 * request, so a line names its operation by a **client key** rather than an
 * id: an operation the server already has keys by its own `key` (its id as
 * text) and sends its `id` so it is kept; a new one gets a key of its own
 * (`new-<n>`) and no id. Lines send `operationKey`.
 */

/** The fields of the BoM itself, as the editor holds them. */
export interface BomHeader {
  variant?: ManufacturingVariantRef;
  code: string;
  outputQuantity: string;
  /** Asked only on create; an existing BoM becomes the default through Make default. */
  isDefault: boolean;
  makeToOrder: boolean;
  leadTimeDays: number | "";
  note: string;
  /** The number of the lot its productions make (manufacturing phase 2 M80). */
  lotPattern: string;
}

/** One operation row. */
export interface EditorOperation {
  /** The client key lines name it by: the server's `key` for a saved one, `new-<n>` for a new one. */
  key: string;
  /** The saved operation's id, sent so the server keeps it; absent for a new one. */
  id?: number;
  sequence: number | "";
  name: string;
  /** The work center it runs at, or null when it targets a group (or nothing is chosen yet). */
  workCenterId: number | null;
  /** The work center group it targets instead of one center (manufacturing phase 2 M118). */
  workCenterGroupId: number | null;
  /** The client keys of the operations it runs after (M112); empty to start at once. */
  dependsOn: string[];
  /** Good units it passes on before its successors may start (M116); empty for none. */
  sendAheadQuantity: string;
  /** Empty for the work center's own setup. */
  setupMinutes: string;
  minutesPerCycle: string;
  instructions: string;
  /** Its quality checks, in order (manufacturing phase 2 M95). */
  checks: EditorCheck[];
}

/** One quality check of an operation (M95): a measure's limits are text, empty for none. */
export interface EditorCheck {
  rowKey: string;
  kind: ManufacturingCheckKind;
  title: string;
  instructions: string;
  frequency: ManufacturingCheckFrequency;
  nominal: string;
  min: string;
  max: string;
  unit: string;
  quarantineOnFail: boolean;
}

/** One component line row. */
export interface EditorLine {
  /** Stable across edits, so a refusal's marks stay on their row. */
  rowKey: string;
  variant?: ManufacturingVariantRef;
  quantity: string;
  scrapPercent: string;
  /** The client key of the operation that consumes it, null for none. */
  operationKey: string | null;
  sortOrder: number | "";
  note: string;
}

export interface BomEditorState {
  header: BomHeader;
  operations: EditorOperation[];
  lines: EditorLine[];
}

let nextKey = 0;

/** A client key no operation of `operations` has: `new-1`, `new-2` … */
export const newOperationKey = (operations: readonly Pick<EditorOperation, "key">[]): string => {
  let key: string;
  do key = `new-${++nextKey}`;
  while (operations.some((operation) => operation.key === key));
  return key;
};

const nextStep = (values: readonly (number | "")[]) =>
  values.reduce<number>((max, value) => (typeof value === "number" && value > max ? value : max), 0) + 10;

/** The operation last in sequence, the one a new operation runs after. */
const lastOperation = (operations: readonly EditorOperation[]) =>
  operations.reduce<EditorOperation | undefined>(
    (last, operation) =>
      last === undefined || (typeof operation.sequence === "number" && operation.sequence >= Number(last.sequence))
        ? operation
        : last,
    undefined,
  );

/**
 * A new operation, after the others in sequence (steps of 10, as routings
 * number them), depending on the last of them — the line phase 1 ran (M112).
 */
export const newOperation = (operations: readonly EditorOperation[]): EditorOperation => ({
  key: newOperationKey(operations),
  sequence: nextStep(operations.map((operation) => operation.sequence)),
  name: "",
  workCenterId: null,
  workCenterGroupId: null,
  dependsOn: (() => {
    const last = lastOperation(operations);
    return last ? [last.key] : [];
  })(),
  sendAheadQuantity: "",
  setupMinutes: "",
  minutesPerCycle: "",
  instructions: "",
  checks: [],
});

/** A new check: pass or fail, recorded once per work order. */
export const newCheck = (): EditorCheck => ({
  rowKey: `check-${++nextKey}`,
  kind: "pass_fail",
  title: "",
  instructions: "",
  frequency: "each_work_order",
  nominal: "",
  min: "",
  max: "",
  unit: "",
  quarantineOnFail: false,
});

/** A new component line, after the others. */
export const newLine = (lines: readonly EditorLine[], variant?: ManufacturingVariantRef): EditorLine => ({
  rowKey: `line-${++nextKey}`,
  variant,
  quantity: variant ? "1" : "",
  scrapPercent: "",
  operationKey: null,
  sortOrder: nextStep(lines.map((line) => line.sortOrder)),
  note: "",
});

/** An empty editor: one output of the variant, no operations, no lines. */
export const emptyEditor = (variant?: ManufacturingVariantRef): BomEditorState => ({
  header: {
    variant,
    code: "",
    outputQuantity: "1",
    isDefault: false,
    makeToOrder: false,
    leadTimeDays: 0,
    note: "",
    lotPattern: "{MO}",
  },
  operations: [],
  lines: [],
});

/** A saved BoM as the editor's state: each operation keyed by its server key, each line naming it by that key. */
export const editorOf = (bom: ManufacturingBom): BomEditorState => ({
  header: {
    variant: bom.variant,
    code: bom.code ?? "",
    outputQuantity: bom.outputQuantity,
    isDefault: bom.isDefault,
    makeToOrder: bom.makeToOrder,
    leadTimeDays: bom.leadTimeDays,
    note: bom.note,
    lotPattern: bom.lotPattern,
  },
  operations: [...bom.operations]
    .sort((a, b) => a.sequence - b.sequence)
    .map((operation) => ({
      key: operation.key,
      id: operation.id,
      sequence: operation.sequence,
      name: operation.name,
      workCenterId: operation.workCenterId,
      workCenterGroupId: operation.workCenterGroupId,
      dependsOn: [...operation.dependsOn],
      sendAheadQuantity: operation.sendAheadQuantity ?? "",
      setupMinutes: operation.setupMinutes ?? "",
      minutesPerCycle: operation.minutesPerCycle,
      instructions: operation.instructions,
      checks: [...(operation.checks ?? [])]
        .sort((a, b) => a.sequence - b.sequence)
        .map((check) => ({
          rowKey: `check-${++nextKey}`,
          kind: check.kind,
          title: check.title,
          instructions: check.instructions,
          frequency: check.frequency,
          nominal: check.nominal ?? "",
          min: check.min ?? "",
          max: check.max ?? "",
          unit: check.unit ?? "",
          quarantineOnFail: check.quarantineOnFail,
        })),
    })),
  lines: [...bom.lines]
    .sort((a, b) => a.sortOrder - b.sortOrder)
    .map((line) => ({
      rowKey: `line-${++nextKey}`,
      variant: line.variant,
      quantity: line.quantity,
      // No scrap reads as an empty field, not as a 0 to type over.
      scrapPercent: isPositiveDecimal(line.scrapPercent) ? line.scrapPercent : "",
      operationKey:
        line.operationKey ??
        (line.operationId === null
          ? null
          : (bom.operations.find((operation) => operation.id === line.operationId)?.key ?? null)),
      sortOrder: line.sortOrder,
      note: line.note,
    })),
});

/**
 * The editor without one operation; every line that named it no longer names
 * any (M13), and every operation that ran after it runs after what it ran
 * after instead, so the graph keeps its order (M112).
 */
export const removeOperation = (state: BomEditorState, key: string): BomEditorState => ({
  ...state,
  operations: state.operations
    .filter((operation) => operation.key !== key)
    .map((operation) => {
      if (!operation.dependsOn.includes(key)) return operation;
      const removed = state.operations.find((one) => one.key === key);
      const inherited = (removed?.dependsOn ?? []).filter((one) => one !== operation.key);
      return {
        ...operation,
        dependsOn: [...new Set([...operation.dependsOn.filter((one) => one !== key), ...inherited])],
      };
    }),
  lines: state.lines.map((line) => (line.operationKey === key ? { ...line, operationKey: null } : line)),
});

const operationBody = (
  operation: EditorOperation,
  index: number,
  operationKeys: ReadonlySet<string>,
): ManufacturingBomOperationInput => ({
  key: operation.key,
  ...(operation.id === undefined ? {} : { id: operation.id }),
  sequence: operation.sequence === "" ? (index + 1) * 10 : operation.sequence,
  name: operation.name.trim(),
  ...(operation.workCenterGroupId === null
    ? { workCenterId: operation.workCenterId ?? 0 }
    : { workCenterGroupId: operation.workCenterGroupId }),
  // Always sent: the editor shows the graph it saves (M112). A key it no longer has is none.
  dependsOn: operation.dependsOn.filter((key) => key !== operation.key && operationKeys.has(key)),
  ...(operation.sendAheadQuantity.trim() === ""
    ? {}
    : { sendAheadQuantity: normalizeDecimal(operation.sendAheadQuantity) }),
  ...(operation.setupMinutes.trim() === "" ? {} : { setupMinutes: normalizeDecimal(operation.setupMinutes) }),
  minutesPerCycle: normalizeDecimal(operation.minutesPerCycle),
  instructions: operation.instructions,
  checks: operation.checks.map(checkBody),
});

/** A limit as sent: its own text trimmed, absent when empty. */
const limit = (value: string) => (value.trim() === "" ? undefined : value.trim().replace(",", "."));

const checkBody = (check: EditorCheck): ManufacturingCheckTemplateInput => {
  const measure = check.kind === "measure";
  const body: ManufacturingCheckTemplateInput = {
    kind: check.kind,
    title: check.title.trim(),
    frequency: check.frequency,
    quarantineOnFail: check.quarantineOnFail,
  };
  if (check.instructions.trim() !== "") body.instructions = check.instructions;
  if (measure) {
    const [nominal, min, max, unit] = [limit(check.nominal), limit(check.min), limit(check.max), check.unit.trim()];
    if (nominal !== undefined) body.nominal = nominal;
    if (min !== undefined) body.min = min;
    if (max !== undefined) body.max = max;
    if (unit !== "") body.unit = unit;
  }
  return body;
};

/** Whether text is a measure: a decimal of either sign with at most four decimals. */
export const isMeasure = (value: string) => /^[-+]?[0-9]+([.,][0-9]{1,4})?$/.test(value.trim());

const lineBody = (line: EditorLine, index: number, operationKeys: ReadonlySet<string>): ManufacturingBomLineInput => ({
  variantId: line.variant?.id ?? 0,
  quantity: normalizeDecimal(line.quantity),
  scrapPercent: line.scrapPercent.trim() === "" ? "0" : normalizeDecimal(line.scrapPercent),
  // A key the editor no longer has is no key: the server would refuse it (operation_key_unknown).
  operationKey: line.operationKey !== null && operationKeys.has(line.operationKey) ? line.operationKey : null,
  sortOrder: line.sortOrder === "" ? (index + 1) * 10 : line.sortOrder,
  note: line.note.trim(),
});

const shared = (state: BomEditorState) => {
  const keys = new Set(state.operations.map((operation) => operation.key));
  return {
    code: state.header.code.trim() === "" ? null : state.header.code.trim(),
    outputQuantity: normalizeDecimal(state.header.outputQuantity),
    makeToOrder: state.header.makeToOrder,
    leadTimeDays: state.header.leadTimeDays === "" ? 0 : state.header.leadTimeDays,
    note: state.header.note.trim(),
    lotPattern: state.header.lotPattern.trim() === "" ? "{MO}" : state.header.lotPattern.trim(),
    operations: state.operations.map((operation, index) => operationBody(operation, index, keys)),
    lines: state.lines.map((line, index) => lineBody(line, index, keys)),
  };
};

/** A new BoM's request: the variant, and whether it becomes its variant's default. */
export const bomCreateBody = (state: BomEditorState): ManufacturingBomInput => ({
  variantId: state.header.variant?.id ?? 0,
  ...shared(state),
  isDefault: state.header.isDefault,
});

/** A saved BoM's replacement under the revision it was read at; its variant and default are not changed here. */
export const bomUpdateBody = (state: BomEditorState, revision: number): ManufacturingBomUpdate => ({
  ...shared(state),
  revision,
});

/**
 * What the editor can tell before it sends, keyed by the same camelCase
 * paths the server answers field errors with (`lines[2].quantity`), so the
 * two are shown the same way. Empty when the BoM can be sent; the server
 * judges the rest — goods, cycles, the site (M10–M14).
 */
export const editorProblems = (state: BomEditorState): Record<string, string> => {
  const problems: Record<string, string> = {};
  if (!state.header.variant) problems.variantId = "required";
  if (!isDecimal(state.header.outputQuantity) || !isPositiveDecimal(state.header.outputQuantity))
    problems.outputQuantity = "quantity";
  if (state.header.code.trim().length > 32) problems.code = "codeLength";
  state.operations.forEach((operation, index) => {
    const at = `operations[${index}]`;
    if (operation.name.trim() === "") problems[`${at}.name`] = "required";
    if (operation.workCenterId === null && operation.workCenterGroupId === null)
      problems[`${at}.workCenterId`] = "required";
    if (
      operation.sendAheadQuantity.trim() !== "" &&
      (!isDecimal(operation.sendAheadQuantity) || !isPositiveDecimal(operation.sendAheadQuantity))
    )
      problems[`${at}.sendAheadQuantity`] = "quantity";
    // Minutes are stored with two decimals, as the server judges them.
    if (!isDecimal(operation.minutesPerCycle, 2) || !isPositiveDecimal(operation.minutesPerCycle))
      problems[`${at}.minutesPerCycle`] = "minutes";
    if (operation.setupMinutes.trim() !== "" && !isDecimal(operation.setupMinutes, 2))
      problems[`${at}.setupMinutes`] = "setupMinutes";
    operation.checks.forEach((check, checkIndex) => {
      const path = `${at}.checks[${checkIndex}]`;
      if (check.title.trim() === "") problems[`${path}.title`] = "required";
      if (check.kind !== "measure") return;
      for (const field of ["nominal", "min", "max"] as const)
        if (check[field].trim() !== "" && !isMeasure(check[field])) problems[`${path}.${field}`] = "measure";
      if (check.min.trim() === "" && check.max.trim() === "") problems[`${path}.min`] = "limits";
    });
  });
  if (state.operations.length > 0 && operationLevels(state.operations) === null) problems.operations = "cycle";
  state.lines.forEach((line, index) => {
    const at = `lines[${index}]`;
    if (!line.variant) problems[`${at}.variantId`] = "required";
    if (!isDecimal(line.quantity) || !isPositiveDecimal(line.quantity)) problems[`${at}.quantity`] = "quantity";
    if (
      line.scrapPercent.trim() !== "" &&
      (!isDecimal(line.scrapPercent, 2) || compareDecimal(line.scrapPercent, "99.99") > 0)
    )
      problems[`${at}.scrapPercent`] = "scrap";
  });
  return problems;
};

/**
 * The operations' graph in levels (M112), for the editor's preview: the
 * first level is what can start at once, each next one what runs after the
 * levels before it — operations of one level run side by side. Null when
 * the operations depend on each other in a circle (operation_cycle).
 */
export const operationLevels = (operations: readonly EditorOperation[]): EditorOperation[][] | null => {
  const keys = new Set(operations.map((operation) => operation.key));
  const placed = new Set<string>();
  const levels: EditorOperation[][] = [];
  const bySequence = [...operations].sort((a, b) => Number(a.sequence) - Number(b.sequence));
  while (placed.size < operations.length) {
    const level = bySequence.filter(
      (operation) =>
        !placed.has(operation.key) &&
        operation.dependsOn.every((key) => key === operation.key || !keys.has(key) || placed.has(key)),
    );
    if (level.length === 0) return null;
    for (const operation of level) placed.add(operation.key);
    levels.push(level);
  }
  return levels;
};
