import {
  type Icon,
  IconColumnInsertLeft,
  IconColumnInsertRight,
  IconColumnRemove,
  IconLayoutNavbar,
  IconRowInsertBottom,
  IconRowInsertTop,
  IconRowRemove,
  IconTableOff,
} from "@tabler/icons-react";
import type { ChainedCommands } from "@tiptap/core";
import type { knowledgeCatalog } from "../i18n";

/** The cell menu's commands (knowledge design K18), each a catalog key, an icon and what it runs. */
export const tableCommands: readonly {
  key: string;
  label: keyof (typeof knowledgeCatalog)["en"];
  icon: Icon;
  run: (chain: ChainedCommands) => ChainedCommands;
}[] = [
  { key: "rowBefore", label: "tableRowBefore", icon: IconRowInsertTop, run: (c) => c.addRowBefore() },
  { key: "rowAfter", label: "tableRowAfter", icon: IconRowInsertBottom, run: (c) => c.addRowAfter() },
  { key: "rowDelete", label: "tableRowDelete", icon: IconRowRemove, run: (c) => c.deleteRow() },
  { key: "columnBefore", label: "tableColumnBefore", icon: IconColumnInsertLeft, run: (c) => c.addColumnBefore() },
  { key: "columnAfter", label: "tableColumnAfter", icon: IconColumnInsertRight, run: (c) => c.addColumnAfter() },
  { key: "columnDelete", label: "tableColumnDelete", icon: IconColumnRemove, run: (c) => c.deleteColumn() },
  { key: "headerRow", label: "tableHeaderRow", icon: IconLayoutNavbar, run: (c) => c.toggleHeaderRow() },
  { key: "tableDelete", label: "tableDelete", icon: IconTableOff, run: (c) => c.deleteTable() },
];
