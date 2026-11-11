import type {
  ManufacturingOrder,
  ManufacturingOrderInput,
  ManufacturingOrderUpdate,
  ManufacturingPriority,
} from "../api/orders";
import type { ManufacturingPerson } from "../api/people";
import type { ManufacturingVariantRef } from "../api/variants";
import { isDecimal, isPositiveDecimal, normalizeDecimal } from "./decimal";

/**
 * What the order form holds (M17): text as typed, ids as chosen. A null
 * BoM, site or output location is the default the form shows — the variant's
 * default BoM, the BoM's site (else the first), the site's root — and is
 * resolved before anything is sent. The planned window is no field: only
 * planning sets it (M34).
 */
export interface OrderValues {
  variant: ManufacturingVariantRef | undefined;
  bomId: number | null;
  quantity: string;
  siteId: number | null;
  componentLocationId: number | null;
  outputLocationId: number | null;
  dueDate: string;
  /** `YYYY-MM-DD`, the day planning may start from (M35a); empty takes the server's default. */
  requestedStart: string;
  priority: ManufacturingPriority;
  responsible: ManufacturingPerson | undefined;
  note: string;
}

export const orderValuesOf = (order?: ManufacturingOrder): OrderValues =>
  order
    ? {
        variant: order.variant,
        bomId: order.bomId,
        quantity: order.quantity,
        siteId: order.siteId,
        componentLocationId: order.componentLocationId,
        outputLocationId: order.outputLocationId,
        dueDate: order.dueDate ?? "",
        requestedStart: order.requestedStart ?? "",
        priority: order.priority,
        responsible: order.responsible ?? undefined,
        note: order.note,
      }
    : {
        variant: undefined,
        bomId: null,
        quantity: "1",
        siteId: null,
        componentLocationId: null,
        outputLocationId: null,
        dueDate: "",
        requestedStart: "",
        priority: "normal",
        responsible: undefined,
        note: "",
      };

/** The problems the form can see before sending, by field: the key of each problem's words (`problem_<key>`). */
export const orderProblems = (values: OrderValues): Record<string, string> => {
  const problems: Record<string, string> = {};
  if (!values.variant) problems.variant = "required";
  if (!values.quantity.trim()) problems.quantity = "required";
  else if (!isDecimal(values.quantity) || !isPositiveDecimal(values.quantity)) problems.quantity = "quantity";
  return problems;
};

/** The defaults the form resolved for what was left at its default. */
export interface OrderDefaults {
  bomId: number | null;
  siteId: number | null;
  outputLocationId: number | null;
}

const common = (values: OrderValues) => ({
  componentLocationId: values.componentLocationId,
  dueDate: values.dueDate || null,
  priority: values.priority,
  responsibleUserId: values.responsible?.id ?? null,
  note: values.note.trim(),
});

/** A new draft (`POST /orders`); a default the form could not resolve is left to the server. */
export const orderInputOf = (values: OrderValues, defaults: OrderDefaults): ManufacturingOrderInput => {
  const bomId = values.bomId ?? defaults.bomId;
  const siteId = values.siteId ?? defaults.siteId;
  const outputLocationId = values.outputLocationId ?? defaults.outputLocationId;
  return {
    variantId: values.variant?.id ?? 0,
    quantity: normalizeDecimal(values.quantity),
    ...(bomId !== null ? { bomId } : {}),
    ...(siteId !== null ? { siteId } : {}),
    ...(outputLocationId !== null ? { outputLocationId } : {}),
    ...(values.requestedStart ? { requestedStart: values.requestedStart } : {}),
    ...common(values),
  };
};

/**
 * The order's fields replaced as a whole (`PUT /orders/{id}`): a draft sends
 * what was changed; a confirmed or in-progress order sends its BoM, quantity
 * and site unchanged, since only a draft may change them (order_not_draft).
 */
export const orderUpdateOf = (
  values: OrderValues,
  order: ManufacturingOrder,
  defaults: OrderDefaults,
): ManufacturingOrderUpdate => {
  const draft = order.status === "draft";
  return {
    revision: order.revision,
    bomId: draft ? (values.bomId ?? defaults.bomId ?? order.bomId) : order.bomId,
    quantity: draft ? normalizeDecimal(values.quantity) : order.quantity,
    siteId: draft ? (values.siteId ?? defaults.siteId ?? order.siteId) : order.siteId,
    outputLocationId: values.outputLocationId ?? defaults.outputLocationId,
    // Always sent: a nullable field left out is cleared, and empty takes the default (M35a).
    requestedStart: values.requestedStart || null,
    ...common(values),
  };
};
