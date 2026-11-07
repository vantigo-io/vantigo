import {
  type Icon,
  IconAlertTriangle,
  IconBlockquote,
  IconBulb,
  IconChevronRight,
  IconCode,
  IconH1,
  IconH2,
  IconH3,
  IconInfoCircle,
  IconLink,
  IconList,
  IconListCheck,
  IconListNumbers,
  IconListTree,
  IconOctagon,
  IconPaperclip,
  IconPhoto,
  IconPilcrow,
  IconSeparator,
  IconTable,
} from "@tabler/icons-react";
import type { ChainedCommands } from "@tiptap/core";
import type { knowledgeCatalog } from "../i18n";

/** What choosing a block does: change the chain, or ask the editor's host for a file to upload. */
export type BlockAction =
  | { kind: "chain"; run: (chain: ChainedCommands) => ChainedCommands }
  | { kind: "upload"; accept: "image" | "file" };

/**
 * A block in the slash menu (knowledge design K18). Its name is a catalog key,
 * so it shows in the reader's language, but `keywords` hold both languages'
 * words, so `/tab` and `/tabell` find a table in either.
 */
export interface SlashBlock {
  key: string;
  label: keyof (typeof knowledgeCatalog)["en"];
  icon: Icon;
  keywords: readonly string[];
  action: BlockAction;
}

const chain = (run: (chain: ChainedCommands) => ChainedCommands): BlockAction => ({ kind: "chain", run });

/** Every block the editor has, in the slash menu's order. */
export const slashBlocks: readonly SlashBlock[] = [
  {
    key: "paragraph",
    label: "blockText",
    icon: IconPilcrow,
    keywords: ["text", "paragraph", "plain", "tekst", "avsnitt", "brødtekst"],
    action: chain((c) => c.setParagraph()),
  },
  {
    key: "heading1",
    label: "formatH1",
    icon: IconH1,
    keywords: ["heading", "title", "h1", "overskrift", "tittel"],
    action: chain((c) => c.setHeading({ level: 1 })),
  },
  {
    key: "heading2",
    label: "formatH2",
    icon: IconH2,
    keywords: ["heading", "subtitle", "h2", "overskrift", "undertittel"],
    action: chain((c) => c.setHeading({ level: 2 })),
  },
  {
    key: "heading3",
    label: "formatH3",
    icon: IconH3,
    keywords: ["heading", "h3", "overskrift"],
    action: chain((c) => c.setHeading({ level: 3 })),
  },
  {
    key: "bulletList",
    label: "formatBulletList",
    icon: IconList,
    keywords: ["bullet", "list", "unordered", "punkt", "liste", "kulepunkt"],
    action: chain((c) => c.toggleBulletList()),
  },
  {
    key: "orderedList",
    label: "formatOrderedList",
    icon: IconListNumbers,
    keywords: ["numbered", "ordered", "list", "nummerert", "liste", "tall"],
    action: chain((c) => c.toggleOrderedList()),
  },
  {
    key: "taskList",
    label: "formatTaskList",
    icon: IconListCheck,
    keywords: ["task", "todo", "checkbox", "check", "oppgave", "sjekkliste", "avkrysning", "gjøremål"],
    action: chain((c) => c.toggleTaskList()),
  },
  {
    key: "toggle",
    label: "blockToggle",
    icon: IconChevronRight,
    keywords: [
      "toggle",
      "details",
      "collapse",
      "expand",
      "fold",
      "sammenleggbar",
      "detaljer",
      "skjul",
      "vis",
      "trekkspill",
    ],
    action: chain((c) => c.setDetails()),
  },
  {
    key: "calloutInfo",
    label: "blockCalloutInfo",
    icon: IconInfoCircle,
    keywords: ["callout", "info", "note", "box", "merknad", "boks", "informasjon", "notat"],
    action: chain((c) => c.setCallout("info")),
  },
  {
    key: "calloutTip",
    label: "blockCalloutTip",
    icon: IconBulb,
    keywords: ["callout", "tip", "hint", "idea", "merknad", "tips", "råd", "idé"],
    action: chain((c) => c.setCallout("tip")),
  },
  {
    key: "calloutWarning",
    label: "blockCalloutWarning",
    icon: IconAlertTriangle,
    keywords: ["callout", "warning", "caution", "merknad", "advarsel", "obs", "forsiktig"],
    action: chain((c) => c.setCallout("warning")),
  },
  {
    key: "calloutDanger",
    label: "blockCalloutDanger",
    icon: IconOctagon,
    keywords: ["callout", "danger", "error", "important", "merknad", "fare", "viktig", "feil"],
    action: chain((c) => c.setCallout("danger")),
  },
  {
    key: "quote",
    label: "formatQuote",
    icon: IconBlockquote,
    keywords: ["quote", "blockquote", "citation", "sitat", "sitere"],
    action: chain((c) => c.toggleBlockquote()),
  },
  {
    key: "codeBlock",
    label: "blockCode",
    icon: IconCode,
    keywords: ["code", "snippet", "pre", "kode", "kodeblokk", "programkode"],
    action: chain((c) => c.setCodeBlock()),
  },
  {
    key: "table",
    label: "blockTable",
    icon: IconTable,
    keywords: ["table", "grid", "columns", "rows", "tabell", "rutenett", "kolonner", "rader"],
    action: chain((c) => c.insertTable({ rows: 3, cols: 3, withHeaderRow: true })),
  },
  {
    key: "divider",
    label: "blockDivider",
    icon: IconSeparator,
    keywords: ["divider", "rule", "line", "separator", "hr", "skillelinje", "linje", "strek"],
    action: chain((c) => c.setHorizontalRule()),
  },
  {
    key: "tableOfContents",
    label: "blockToc",
    icon: IconListTree,
    keywords: ["table of contents", "toc", "contents", "outline", "innholdsfortegnelse", "innhold", "oversikt"],
    action: chain((c) => c.insertContent({ type: "tableOfContents" })),
  },
  {
    key: "image",
    label: "blockImage",
    icon: IconPhoto,
    keywords: ["image", "picture", "photo", "upload", "bilde", "foto", "last opp"],
    action: { kind: "upload", accept: "image" },
  },
  {
    key: "file",
    label: "blockFile",
    icon: IconPaperclip,
    keywords: ["file", "attachment", "upload", "pdf", "fil", "vedlegg", "last opp"],
    action: { kind: "upload", accept: "file" },
  },
  {
    key: "articleLink",
    label: "blockArticleLink",
    icon: IconLink,
    keywords: ["link", "page", "article", "wiki", "[[", "lenke", "side", "artikkel"],
    action: chain((c) => c.insertContent("[[")),
  },
];

const fold = (text: string) => text.toLocaleLowerCase("nb").normalize("NFKD").replace(/\p{M}/gu, "");

/**
 * The blocks matching what was typed after `/`: by the name the reader sees,
 * or by a keyword in either language. Matches at a word's start rank first.
 */
export const filterBlocks = (
  blocks: readonly SlashBlock[],
  query: string,
  name: (block: SlashBlock) => string,
): SlashBlock[] => {
  const wanted = fold(query.trim());
  if (!wanted) return [...blocks];
  const scored: { block: SlashBlock; score: number }[] = [];
  for (const block of blocks) {
    const words = [name(block), ...block.keywords].map(fold);
    const starts = words.some(
      (word) => word.startsWith(wanted) || word.split(/\s+/).some((part) => part.startsWith(wanted)),
    );
    const contains = !starts && words.some((word) => word.includes(wanted));
    if (starts || contains) scored.push({ block, score: starts ? 0 : 1 });
  }
  return scored.sort((a, b) => a.score - b.score).map(({ block }) => block);
};
