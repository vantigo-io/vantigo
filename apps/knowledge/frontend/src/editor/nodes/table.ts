import { Table, TableCell, TableHeader, TableRow } from "@tiptap/extension-table";

/**
 * A table cell's attributes as the allowlist takes them (knowledge design
 * K18): `colspan`, `rowspan` and `colwidth`. TipTap's own cell also has an
 * `align`, which the editor offers nowhere, so it is left out of the schema.
 */
const withoutAlign = <T extends Record<string, unknown>>(attributes: T | undefined) => {
  const rest: Record<string, unknown> = { ...attributes };
  delete rest.align;
  return rest as Omit<T, "align">;
};

const Cell = TableCell.extend({
  addAttributes() {
    return withoutAlign(this.parent?.());
  },
});

const Header = TableHeader.extend({
  addAttributes() {
    return withoutAlign(this.parent?.());
  },
});

/** Tables (K18): resizable columns, a header row, cells that span; the cell menu is `TableMenu`. */
export const tableExtensions = [
  Table.configure({ resizable: true, allowTableNodeSelection: true }),
  TableRow,
  Header,
  Cell,
];
