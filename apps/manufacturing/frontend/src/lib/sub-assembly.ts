import type { ManufacturingOrder, ManufacturingOrderComponent } from "../api/orders";
import { compareDecimal } from "./decimal";

/**
 * The component lines a sub-assembly order can be made for (M47): those
 * whose variant has a default active BoM and that miss something.
 */
export const subAssemblyLines = (order: ManufacturingOrder): ManufacturingOrderComponent[] =>
  order.components.filter(
    (line) => line.id != null && line.defaultBomId != null && compareDecimal(line.missing, "0") > 0,
  );
