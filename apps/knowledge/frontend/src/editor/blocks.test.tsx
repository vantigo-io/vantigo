import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Editor, JSONContent } from "@tiptap/core";
import { describe, expect, it, vi } from "vitest";
import { knowledgeCatalog } from "../i18n";
import { renderEditor } from "../test/editor";
import { filterBlocks, slashBlocks } from "./blocks";
import { documentHeadings } from "./nodes/headings";
import { tableCommands } from "./table-commands";

/** The document as plain JSON, without TipTap's typed node shapes. */
const docOf = (editor: Editor) => editor.getJSON() as JSONContent;

const en = (block: (typeof slashBlocks)[number]) => knowledgeCatalog.en[block.label];
const nb = (block: (typeof slashBlocks)[number]) => knowledgeCatalog.nb[block.label];

const keys = (query: string, name = en) => filterBlocks(slashBlocks, query, name).map((block) => block.key);

const typeSlash = (editor: Editor, text: string) =>
  act(() => {
    editor.chain().focus().insertContent(text).run();
  });

const pressKey = (editor: Editor, key: string) =>
  act(() => {
    fireEvent.keyDown(editor.view.dom, { key });
  });

describe("the slash menu's blocks", () => {
  it("names every block in both languages", () => {
    for (const block of slashBlocks) {
      expect(knowledgeCatalog.en[block.label], block.key).toBeTruthy();
      expect(knowledgeCatalog.nb[block.label], block.key).toBeTruthy();
    }
  });

  it("finds a block by its name or a keyword in either language", () => {
    expect(keys("tab")).toEqual(expect.arrayContaining(["table", "tableOfContents"]));
    expect(keys("tabell")).toContain("table");
    expect(keys("tabell", nb)).toContain("table");
    expect(keys("overskrift")).toEqual(["heading1", "heading2", "heading3"]);
    expect(keys("heading", nb)).toEqual(["heading1", "heading2", "heading3"]);
    expect(keys("advarsel")).toEqual(["calloutWarning"]);
    expect(keys("gjøre")).toEqual(["taskList"]);
    expect(keys("FORSIKTIG")).toEqual(["calloutWarning"]);
    expect(keys("zzz")).toEqual([]);
    expect(keys("")).toHaveLength(slashBlocks.length);
  });

  it("ranks a match at a word's start before one inside a word", () => {
    expect(keys("ode")[0]).toBe("codeBlock");
    expect(keys("code")[0]).toBe("codeBlock");
  });
});

describe("the slash menu in the editor", () => {
  it("opens on / at the start of a line, filters, and inserts the chosen block", async () => {
    const { editor } = await renderEditor();
    typeSlash(editor, "/");
    const menu = await screen.findByRole("listbox", { name: "Blocks" });
    expect(within(menu).getAllByRole("option").length).toBeGreaterThan(10);

    typeSlash(editor, "tabell");
    await waitFor(() => expect(within(menu).getAllByRole("option")[0]).toHaveTextContent("Table"));
    pressKey(editor, "Enter");

    await waitFor(() => expect(screen.queryByRole("listbox", { name: "Blocks" })).toBeNull());
    const json = docOf(editor);
    const table = json.content?.find((node) => node.type === "table");
    expect(table?.content).toHaveLength(3);
    expect(table?.content?.[0].content?.[0].type).toBe("tableHeader");
    expect(JSON.stringify(json)).not.toContain("/tabell");
  });

  it("moves through the list with the arrows", async () => {
    const { editor } = await renderEditor();
    typeSlash(editor, "/overskrift");
    const menu = await screen.findByRole("listbox", { name: "Blocks" });
    expect(within(menu).getAllByRole("option")[0]).toHaveAttribute("aria-selected", "true");
    pressKey(editor, "ArrowDown");
    pressKey(editor, "ArrowDown");
    await waitFor(() => expect(within(menu).getAllByRole("option")[2]).toHaveAttribute("aria-selected", "true"));
    pressKey(editor, "ArrowDown");
    await waitFor(() => expect(within(menu).getAllByRole("option")[0]).toHaveAttribute("aria-selected", "true"));
    pressKey(editor, "ArrowUp");
    pressKey(editor, "Enter");
    await waitFor(() => expect(docOf(editor).content?.[0]).toMatchObject({ type: "heading", attrs: { level: 3 } }));
  });

  it("does not open in the middle of a line, and closes on Escape", async () => {
    const { editor } = await renderEditor();
    typeSlash(editor, "a/b");
    expect(screen.queryByRole("listbox", { name: "Blocks" })).toBeNull();
    act(() => {
      editor
        .chain()
        .focus()
        .setContent({ type: "doc", content: [{ type: "paragraph" }] })
        .insertContent("/")
        .run();
    });
    await screen.findByRole("listbox", { name: "Blocks" });
    pressKey(editor, "Escape");
    await waitFor(() => expect(screen.queryByRole("listbox", { name: "Blocks" })).toBeNull());
  });

  it("inserts a block by click", async () => {
    const { editor } = await renderEditor();
    typeSlash(editor, "/divider");
    const menu = await screen.findByRole("listbox", { name: "Blocks" });
    await userEvent.click(within(menu).getByRole("option", { name: /Divider/ }));
    await waitFor(() => expect(docOf(editor).content?.some((node) => node.type === "horizontalRule")).toBe(true));
  });
});

describe("callouts", () => {
  it("wraps the line in a callout of the chosen kind, and switches kind and emoji", async () => {
    const { editor } = await renderEditor({
      content: { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "Remember" }] }] },
    });
    act(() => {
      editor.chain().focus().setCallout("warning").run();
    });
    expect(docOf(editor).content?.[0]).toMatchObject({
      type: "callout",
      attrs: { kind: "warning", emoji: null },
      content: [{ type: "paragraph", content: [{ type: "text", text: "Remember" }] }],
    });

    await userEvent.click(await screen.findByRole("button", { name: "Callout: Warning. Change its kind or emoji" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Tip" }));
    await waitFor(() => expect(docOf(editor).content?.[0].attrs?.kind).toBe("tip"));

    await userEvent.click(screen.getByRole("button", { name: "Callout: Tip. Change its kind or emoji" }));
    await userEvent.click(await screen.findByRole("button", { name: "🔥" }));
    await waitFor(() => expect(docOf(editor).content?.[0].attrs?.emoji).toBe("🔥"));
  });

  it("shows a reader the callout without the switcher", async () => {
    await renderEditor({
      editable: false,
      content: {
        type: "doc",
        content: [{ type: "callout", attrs: { kind: "danger", emoji: null }, content: [{ type: "paragraph" }] }],
      },
    });
    expect(await screen.findByLabelText("Danger")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Change its kind/ })).toBeNull();
  });
});

describe("tables", () => {
  const table = {
    type: "doc",
    content: [
      {
        type: "table",
        content: [
          { type: "tableRow", content: [{ type: "tableHeader", content: [{ type: "paragraph" }] }] },
          { type: "tableRow", content: [{ type: "tableCell", content: [{ type: "paragraph" }] }] },
        ],
      },
    ],
  };

  const shape = (editor: Editor) => {
    const node = docOf(editor).content?.find((block) => block.type === "table");
    return { rows: node?.content?.length ?? 0, columns: node?.content?.[0].content?.length ?? 0 };
  };

  it("adds and removes rows and columns from the cell menu's commands", async () => {
    const { editor } = await renderEditor({ content: table });
    act(() => {
      editor.chain().focus().setTextSelection(4).run();
    });
    const run = (key: string) =>
      act(() => {
        const command = tableCommands.find((entry) => entry.key === key);
        command?.run(editor.chain().focus()).run();
      });
    run("rowAfter");
    expect(shape(editor)).toEqual({ rows: 3, columns: 1 });
    run("columnAfter");
    expect(shape(editor)).toEqual({ rows: 3, columns: 2 });
    run("columnDelete");
    expect(shape(editor)).toEqual({ rows: 3, columns: 1 });
    run("rowDelete");
    expect(shape(editor)).toEqual({ rows: 2, columns: 1 });
    run("tableDelete");
    expect(shape(editor)).toEqual({ rows: 0, columns: 0 });
  });

  it("names every cell command in both languages", () => {
    for (const command of tableCommands) {
      expect(knowledgeCatalog.en[command.label]).toBeTruthy();
      expect(knowledgeCatalog.nb[command.label]).toBeTruthy();
    }
  });

  it("keeps only the cell attributes the allowlist takes", async () => {
    const { editor } = await renderEditor({ content: table });
    const cell = docOf(editor).content?.[0].content?.[1].content?.[0];
    expect(Object.keys(cell?.attrs ?? {}).sort()).toEqual(["colspan", "colwidth", "rowspan"]);
  });
});

describe("the table of contents", () => {
  const doc = {
    type: "doc",
    content: [
      { type: "tableOfContents" },
      { type: "heading", attrs: { level: 1, id: "h-intro" }, content: [{ type: "text", text: "Intro" }] },
      { type: "paragraph", content: [{ type: "text", text: "Body" }] },
      { type: "heading", attrs: { level: 2, id: "h-steps" }, content: [{ type: "text", text: "Steps" }] },
      { type: "heading", attrs: { level: 2 } },
    ],
  };

  it("lists the headings with text, as links to their block ids", async () => {
    const { editor } = await renderEditor({ content: doc });
    const nav = await screen.findByRole("navigation", { name: "Table of contents" });
    const links = within(nav).getAllByRole("link");
    expect(links.map((link) => link.textContent)).toEqual(["Intro", "Steps"]);
    expect(links[0]).toHaveAttribute("href", "#h-intro");
    expect(documentHeadings(editor.state.doc).map((heading) => heading.level)).toEqual([1, 2]);
  });

  it("follows the headings as they change, and scrolls to one when clicked", async () => {
    const { editor } = await renderEditor({ content: doc });
    const nav = await screen.findByRole("navigation", { name: "Table of contents" });
    act(() => {
      editor
        .chain()
        .focus("end")
        .insertContent({ type: "heading", attrs: { level: 3 }, content: [{ type: "text", text: "Last" }] })
        .run();
    });
    await waitFor(() =>
      expect(
        within(nav)
          .getAllByRole("link")
          .map((link) => link.textContent),
      ).toContain("Last"),
    );
    const heading = editor.view.dom.querySelector("h2");
    const scrolled = vi.fn();
    if (heading) heading.scrollIntoView = scrolled;
    await userEvent.click(within(nav).getByRole("link", { name: "Steps" }));
    expect(scrolled).toHaveBeenCalled();
  });
});

describe("toggles and code blocks", () => {
  it("inserts a toggle with a summary and content, its open state stored", async () => {
    const { editor } = await renderEditor();
    typeSlash(editor, "/toggle");
    await screen.findByRole("listbox", { name: "Blocks" });
    pressKey(editor, "Enter");
    await waitFor(() =>
      expect(docOf(editor).content?.[0]).toMatchObject({
        type: "details",
        content: [{ type: "detailsSummary" }, { type: "detailsContent" }],
      }),
    );
    expect(docOf(editor).content?.[0].attrs).toHaveProperty("open");
  });

  it("chooses a code block's language, stored as a short id", async () => {
    const { editor } = await renderEditor();
    act(() => {
      editor.chain().focus().setCodeBlock().insertContent("select 1").run();
    });
    const select = await screen.findByRole("combobox", { name: "Language" });
    await userEvent.selectOptions(select, "sql");
    await waitFor(() =>
      expect(docOf(editor).content?.[0]).toMatchObject({ type: "codeBlock", attrs: { language: "sql" } }),
    );
  });

  it("gives blocks stable ids, without counting that as an edit to save", async () => {
    const onUpdate = vi.fn();
    const { editor } = await renderEditor({
      content: { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "x" }] }] },
      onUpdate,
    });
    await waitFor(() => expect(docOf(editor).content?.[0].attrs?.id).toMatch(/^[0-9a-f-]{36}$/));
    expect(onUpdate).not.toHaveBeenCalled();
    const id = docOf(editor).content?.[0].attrs?.id;
    act(() => {
      editor.chain().focus("end").insertContent(" more").run();
    });
    expect(docOf(editor).content?.[0].attrs?.id).toBe(id);
    expect(onUpdate).toHaveBeenCalled();
  });
});
