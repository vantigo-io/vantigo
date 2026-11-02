import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { conversation } from "../test/server";
import { createAssistantContextRegistry, templateCovers } from "./context";
import { groupConversations } from "./groups";
import { classifyLink } from "./links";
import { clampPanelWidth, PANEL_MAX_WIDTH, PANEL_MIN_WIDTH } from "./panel";
import { filterGroups } from "./permissions";
import { isAssistantShortcut, shouldIgnoreAssistantShortcut } from "./shortcut";

describe("groupConversations", () => {
  const now = new Date("2026-11-01T12:00:00");
  const at = (iso: string, extra = {}) => conversation({ id: iso, lastActivityAt: iso, ...extra });

  it("puts pinned first and groups the rest by Today, Yesterday, Last 7 days and Older", () => {
    const groups = groupConversations(
      [
        at("2026-10-01T09:00:00", { pinned: true, id: "pinned" }),
        at("2026-11-01T08:00:00", { id: "today" }),
        at("2026-10-31T23:00:00", { id: "yesterday" }),
        at("2026-10-27T10:00:00", { id: "week" }),
        at("2026-09-01T10:00:00", { id: "older" }),
      ],
      now,
    );
    expect(groups.map((group) => [group.key, group.conversations.map((c) => c.id)])).toEqual([
      ["pinned", ["pinned"]],
      ["today", ["today"]],
      ["yesterday", ["yesterday"]],
      ["week", ["week"]],
      ["older", ["older"]],
    ]);
  });

  it("leaves out empty groups", () => {
    expect(groupConversations([at("2026-11-01T08:00:00")], now).map((group) => group.key)).toEqual(["today"]);
  });
});

describe("the Cmd/Ctrl+J rule", () => {
  const key = (init: KeyboardEventInit) => new KeyboardEvent("keydown", { key: "j", ...init });

  it("is Cmd or Ctrl with J and nothing else", () => {
    expect(isAssistantShortcut(key({ metaKey: true }))).toBe(true);
    expect(isAssistantShortcut(key({ ctrlKey: true, key: "J" }))).toBe(true);
    expect(isAssistantShortcut(key({ ctrlKey: true, shiftKey: true }))).toBe(false);
    expect(isAssistantShortcut(key({ ctrlKey: true, altKey: true }))).toBe(false);
    expect(isAssistantShortcut(key({}))).toBe(false);
  });

  it("is left alone inside editors, code editors and anything that marks itself", () => {
    const inside = (html: string) => {
      document.body.innerHTML = html;
      const target = document.body.querySelector("[data-target]") as Element;
      return shouldIgnoreAssistantShortcut({ defaultPrevented: false, target });
    };
    expect(inside('<div contenteditable="true"><p data-target>x</p></div>')).toBe(true);
    expect(inside('<div class="cm-editor"><div data-target></div></div>')).toBe(true);
    expect(inside('<div class="monaco-editor"><div data-target></div></div>')).toBe(true);
    expect(inside('<div data-assistant-shortcut="ignore"><textarea data-target></textarea></div>')).toBe(true);
    expect(inside("<textarea data-target></textarea>")).toBe(false);
    expect(inside("<input data-target>")).toBe(false);
    // The assistant's own composer is the one place a rich-text parent does not take it back.
    expect(
      inside('<div contenteditable="true"><div data-assistant-panel><textarea data-target></textarea></div></div>'),
    ).toBe(false);
    expect(shouldIgnoreAssistantShortcut({ defaultPrevented: true, target: document.body })).toBe(true);
  });
});

describe("assistantContext", () => {
  const route = (routeId: string, params: Record<string, string> = {}) => ({ routeId, pathname: routeId, params });

  it("covers a route and its children, longest template first", () => {
    expect(templateCovers("/customers/$customerId", "/customers/$customerId/")).toBe(true);
    expect(templateCovers("/customers/$customerId", "/customers/$customerId/invoices")).toBe(true);
    expect(templateCovers("/customers/$customerId", "/customers/import")).toBe(false);

    const registry = createAssistantContextRegistry();
    const client = new QueryClient();
    registry.register("/customers/$customerId", () => ({ label: "Customer" }));
    registry.register("/customers/$customerId/invoices", () => ({ label: "Customer invoices" }));
    expect(registry.resolve(route("/customers/$customerId/invoices"), client)?.label).toBe("Customer invoices");
    expect(registry.resolve(route("/customers/$customerId/"), client)?.label).toBe("Customer");
    expect(registry.resolve(route("/products"), client)).toBeUndefined();
  });

  it("falls through a resolver whose data is not loaded, and can be unregistered", () => {
    const registry = createAssistantContextRegistry();
    const client = new QueryClient();
    registry.register("/a", () => ({ label: "outer" }));
    const off = registry.register("/a/b", () => undefined);
    expect(registry.resolve(route("/a/b"), client)?.label).toBe("outer");
    off();
    expect(registry.resolve(route("/a/b"), client)?.label).toBe("outer");
  });
});

describe("classifyLink", () => {
  it("opens the app's own routes and the documentation, and nothing else", () => {
    expect(classifyLink("/customers/42")).toEqual({ kind: "app", route: "/customers/42" });
    expect(classifyLink("/invoices/overdue?customer=4")).toEqual({
      kind: "app",
      route: "/invoices/overdue?customer=4",
    });
    expect(classifyLink("/assistant")).toEqual({ kind: "app", route: "/assistant" });
    expect(classifyLink("/docs/nb/user/energy/#add")).toMatchObject({ kind: "docs" });
    expect(classifyLink("/docs")).toMatchObject({ kind: "docs" });

    for (const href of [
      "https://example.com",
      "http://example.com",
      "mailto:a@example.com",
      "tel:+4712345678",
      "//evil.example",
      "javascript:alert(1)",
      "/nothing/here",
      "/customersx/1",
      "/customers/../admin",
      "customers/1",
    ])
      expect(classifyLink(href), href).toEqual({ kind: "blocked", address: href });
    expect(classifyLink(undefined)).toEqual({ kind: "blocked" });
  });

  it("keeps the route prefixes in step with the server's prompt", async () => {
    const { default: source } = await import("../../../../server/internal/assistant/prompt.go?raw");
    const server = [...source.matchAll(/\{"(\/[a-z]+)", "/g)].map((match) => match[1]).sort();
    const { APP_ROUTE_PREFIXES } = await import("./links");
    expect([...APP_ROUTE_PREFIXES].sort()).toEqual(server);
  });
});

describe("the panel's width", () => {
  it("stays between its limits", () => {
    expect(clampPanelWidth(10)).toBe(PANEL_MIN_WIDTH);
    expect(clampPanelWidth(5000)).toBe(PANEL_MAX_WIDTH);
    expect(clampPanelWidth(433.4)).toBe(433);
  });
});

describe("filterGroups", () => {
  it("keeps only the groups that still have a permission", () => {
    const groups = [
      {
        module: "a",
        category: "A",
        permissions: [
          { key: "a:1", display: "1", sensitive: true, defaultLevel: "off", effectiveLevel: "off" },
          { key: "a:2", display: "2", sensitive: false, defaultLevel: "change", effectiveLevel: "change" },
        ],
      },
      {
        module: "b",
        category: "B",
        permissions: [{ key: "b:1", display: "3", sensitive: false, defaultLevel: "change", effectiveLevel: "change" }],
      },
    ] as const;
    expect(filterGroups(groups as never, "sensitive").map((group) => group.module)).toEqual(["a"]);
    expect(filterGroups(groups as never, "overridden")).toEqual([]);
    expect(filterGroups(groups as never, "all")).toHaveLength(2);
  });
});
