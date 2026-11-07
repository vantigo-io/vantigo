import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, onTestFinished, vi } from "vitest";
import { fakeServer, ids, jsonResponse, refusal } from "../test/api";
import { renderRoute } from "../test/render";
import { Backlinks } from "./backlinks";
import { ExportDialog, ImportDialog } from "./markdown-dialogs";

const noop = () => undefined;

describe("Export as Markdown", () => {
  it("downloads the page, or with its subpages a zip", async () => {
    const server = fakeServer((call) => {
      if (call.url.startsWith(`/api/v1/knowledge/articles/${ids.routines}/export`))
        return new Response("# Routines", {
          status: 200,
          headers: {
            "Content-Type": call.url.includes("subtree") ? "application/zip" : "text/markdown",
            "Content-Disposition": call.url.includes("subtree") ? 'attachment; filename="Routines.zip"' : "",
          },
        });
      return undefined;
    });
    // jsdom has no object URLs; the download is a click on a link to one.
    const { createObjectURL, revokeObjectURL } = URL;
    URL.createObjectURL = vi.fn(() => "blob:x");
    URL.revokeObjectURL = vi.fn();
    onTestFinished(() => {
      URL.createObjectURL = createObjectURL;
      URL.revokeObjectURL = revokeObjectURL;
    });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    const onClose = vi.fn();
    renderRoute(
      "/here",
      undefined,
      <ExportDialog article={{ id: ids.routines, title: "Routines" }} opened onClose={onClose} />,
    );

    await userEvent.click(await screen.findByRole("button", { name: "Download" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(server.sent("GET", `/api/v1/knowledge/articles/${ids.routines}/export`)).toBeDefined();
    expect(await screen.findByText("Downloaded Routines.md.")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("checkbox", { name: /Include subpages/ }));
    await userEvent.click(screen.getByRole("button", { name: "Download" }));
    await waitFor(() => expect(server.sent("GET", /export\?subtree=true$/)).toBeDefined());
    expect(await screen.findByText("Downloaded Routines.zip.")).toBeInTheDocument();
    expect(click).toHaveBeenCalledTimes(2);
  });
});

describe("Import Markdown", () => {
  it("imports a file under this page and lists what it made and what it could not carry over", async () => {
    const server = fakeServer((call) => {
      if (call.method === "POST" && call.url === "/api/v1/knowledge/import")
        return jsonResponse(201, {
          created: [
            { id: ids.onboarding, title: "Vault" },
            { id: ids.notes, title: "Daily" },
          ],
          warnings: ["Daily.md: an embedded PDF was left out."],
        });
      return undefined;
    });
    renderRoute(
      "/here",
      undefined,
      <ImportDialog article={{ id: ids.routines, permission: "write" }} opened onClose={noop} />,
    );
    const submit = await screen.findByRole("button", { name: "Import" });
    expect(submit).toBeDisabled();
    expect(screen.getByRole("radio", { name: "Under this page" })).toBeChecked();

    const file = new File(["# Vault"], "vault.zip", { type: "application/zip" });
    const input = document.querySelector<HTMLInputElement>('input[type="file"]');
    if (!input) throw new Error("no file input");
    await userEvent.upload(input, file);
    await userEvent.click(submit);

    expect(await screen.findByText("2 pages were imported.", { selector: "p" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Vault" })).toHaveAttribute(
      "href",
      `/knowledge/articles/${ids.onboarding}`,
    );
    expect(screen.getByText("Daily.md: an embedded PDF was left out.")).toBeInTheDocument();
    const body = server.sent("POST", "/api/v1/knowledge/import")?.body as FormData;
    expect(body.get("file")).toBeInstanceOf(File);
    expect(body.get("parentId")).toBe(ids.routines);
    expect(body.get("section")).toBeNull();
  });

  it("imports at the top of a section for a reader, and says why a refused file failed", async () => {
    const server = fakeServer((call) => {
      if (call.method === "POST" && call.url === "/api/v1/knowledge/import")
        return refusal(400, "invalid", { errors: { file: ["The zip holds no Markdown file."] } });
      return undefined;
    });
    renderRoute(
      "/here",
      undefined,
      <ImportDialog article={{ id: ids.routines, permission: "read" }} opened onClose={noop} />,
    );
    await screen.findByRole("button", { name: "Import" });
    expect(screen.queryByRole("radio", { name: "Under this page" })).toBeNull();
    await userEvent.click(screen.getByRole("radio", { name: "At the top of Private" }));
    const input = document.querySelector<HTMLInputElement>('input[type="file"]');
    if (!input) throw new Error("no file input");
    await userEvent.upload(input, new File(["x"], "empty.zip", { type: "application/zip" }));
    await userEvent.click(screen.getByRole("button", { name: "Import" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The zip holds no Markdown file.");
    const body = server.sent("POST", "/api/v1/knowledge/import")?.body as FormData;
    expect(body.get("section")).toBe("private");
    expect(body.get("parentId")).toBeNull();
  });
});

describe("export and import refusals", () => {
  it("says when a page and its subpages are too large to export", async () => {
    fakeServer((call) => (call.url.includes("/export") ? refusal(413, "export_too_large") : undefined));
    renderRoute(
      "/here",
      undefined,
      <ExportDialog article={{ id: ids.routines, title: "Routines" }} opened onClose={noop} />,
    );
    await userEvent.click(await screen.findByRole("button", { name: "Download" }));
    expect(await screen.findByText("This page and its sub-pages are too large to export at once.")).toBeInTheDocument();
  });

  it("says when another import is running", async () => {
    fakeServer((call) =>
      call.url === "/api/v1/knowledge/import"
        ? new Response(JSON.stringify({ title: "Busy", status: 429, code: "import_busy" }), {
            status: 429,
            headers: { "Content-Type": "application/problem+json", "Retry-After": "30" },
          })
        : undefined,
    );
    renderRoute(
      "/here",
      undefined,
      <ImportDialog article={{ id: ids.routines, permission: "write" }} opened onClose={noop} />,
    );
    await screen.findByRole("button", { name: "Import" });
    const input = document.querySelector<HTMLInputElement>('input[type="file"]');
    if (!input) throw new Error("no file input");
    await userEvent.upload(input, new File(["# A"], "a.md", { type: "text/markdown" }));
    await userEvent.click(screen.getByRole("button", { name: "Import" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Another import is running — try again in a moment.");
  });
});

describe("Linked from", () => {
  it("lists the pages that link here, with where they sit", async () => {
    fakeServer((call) => {
      if (call.url === `/api/v1/knowledge/articles/${ids.routines}/backlinks`)
        return jsonResponse(200, [
          { id: ids.onboarding, title: "Onboarding", icon: "🧭", path: [{ id: ids.notes, title: "HR" }] },
        ]);
      return undefined;
    });
    renderRoute("/here", undefined, <Backlinks articleId={ids.routines} />);
    expect(await screen.findByRole("heading", { name: "Linked from" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Onboarding" })).toHaveAttribute(
      "href",
      `/knowledge/articles/${ids.onboarding}`,
    );
    expect(screen.getByText("HR")).toBeInTheDocument();
  });

  it("shows nothing while no page links here", async () => {
    const server = fakeServer((call) => {
      if (call.url.endsWith("/backlinks")) return jsonResponse(200, []);
      return undefined;
    });
    renderRoute("/here", undefined, <Backlinks articleId={ids.routines} />);
    await waitFor(() => expect(server.count("GET", /backlinks$/)).toBe(1));
    expect(screen.queryByRole("heading", { name: "Linked from" })).toBeNull();
  });
});
