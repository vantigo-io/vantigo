import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { KnowledgeArticleAccess } from "../api/articles";
import { fakeServer, ids, jsonResponse, refusal } from "../test/api";
import { renderRoute } from "../test/render";
import { AccessDialog } from "./access-dialog";

const access = (overrides: Partial<KnowledgeArticleAccess> = {}): KnowledgeArticleAccess => ({
  articleId: ids.onboarding,
  section: "workspace",
  internalPermission: null,
  effectivePermission: "read",
  inheritedFrom: { id: ids.routines, title: "Routines" },
  canManage: true,
  members: [
    {
      userId: ids.kari,
      displayName: "Kari Nordmann",
      permission: "write",
      inherited: true,
      inheritedFrom: { id: ids.routines, title: "Routines" },
    },
    { userId: ids.ola, displayName: "Ola Hansen", permission: "read", inherited: false, inheritedFrom: null },
  ],
  ...overrides,
});

const url = `/api/v1/knowledge/articles/${ids.onboarding}/access`;

const open = () =>
  renderRoute("/here", undefined, <AccessDialog articleId={ids.onboarding} isRoot={false} opened onClose={() => {}} />);

describe("the access dialog", () => {
  it("shows the inherited default and rows, and saves only this page's own", async () => {
    const server = fakeServer(({ method, url: at }) => {
      if (at !== url) return undefined;
      return method === "GET" ? jsonResponse(200, access()) : jsonResponse(200, access());
    });
    open();
    const dialog = await screen.findByRole("dialog", { name: "Share" });
    expect(await within(dialog).findByText("Can read, from Routines")).toBeInTheDocument();
    expect(within(dialog).getByText("From Routines")).toBeInTheDocument();
    expect(within(dialog).getByText("Kari Nordmann")).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(server.sent("PUT", url)?.body).toEqual({
        internalPermission: null,
        members: [{ userId: ids.ola, permission: "read" }],
      }),
    );
  });

  it("says a page with its own default does not take the parent pages' people", async () => {
    fakeServer(({ method, url: at }) =>
      method === "GET" && at === url ? jsonResponse(200, access({ internalPermission: "write" })) : undefined,
    );
    open();
    const dialog = await screen.findByRole("dialog", { name: "Share" });
    expect(
      await within(dialog).findByText("This page has its own access. People added on parent pages don't apply here."),
    ).toBeInTheDocument();
    expect(within(dialog).queryByText("From Routines")).toBeNull();
  });

  it("says when the change leaves the caller without access", async () => {
    fakeServer(({ method, url: at }) => {
      if (at !== url) return undefined;
      return method === "GET"
        ? jsonResponse(200, access())
        : jsonResponse(200, access({ members: [], canManage: false, effectivePermission: "none" }));
    });
    const onClose = vi.fn();
    const onLostAccess = vi.fn();
    renderRoute(
      "/here",
      undefined,
      <AccessDialog articleId={ids.onboarding} isRoot={false} opened onClose={onClose} onLostAccess={onLostAccess} />,
    );
    const dialog = await screen.findByRole("dialog", { name: "Share" });
    await userEvent.click(await within(dialog).findByRole("button", { name: "Save" }));
    await waitFor(() => expect(onLostAccess).toHaveBeenCalledTimes(1));
    expect(onClose).toHaveBeenCalled();
  });

  it("says that the last writer cannot be removed", async () => {
    fakeServer(({ method, url: at }) => {
      if (at !== url) return undefined;
      return method === "GET" ? jsonResponse(200, access()) : refusal(400, "last_writer");
    });
    open();
    const dialog = await screen.findByRole("dialog", { name: "Share" });
    await userEvent.click(await within(dialog).findByRole("button", { name: "Remove Ola Hansen" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("Someone must be able to edit the page");
  });

  it("is read-only for someone who may not change it", async () => {
    fakeServer(({ method, url: at }) =>
      method === "GET" && at === url ? jsonResponse(200, access({ canManage: false })) : undefined,
    );
    open();
    const dialog = await screen.findByRole("dialog", { name: "Share" });
    expect(
      await within(dialog).findByText("Only a writer of this page can change who has access."),
    ).toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "Save" })).toBeNull();
    expect(within(dialog).queryByLabelText("Add a person")).toBeNull();
  });

  it("confirms a change that moves the page between Private and the workspace", async () => {
    const server = fakeServer(({ method, url: at, body }) => {
      if (at !== url) return undefined;
      if (method === "GET") return jsonResponse(200, access());
      return (body as { confirmVisibility?: boolean }).confirmVisibility
        ? jsonResponse(200, access())
        : refusal(409, "visibility_change");
    });
    open();
    const dialog = await screen.findByRole("dialog", { name: "Share" });
    await userEvent.click(await within(dialog).findByRole("button", { name: "Save" }));
    const confirm = await screen.findByRole("dialog", { name: "Change who can read this page?" });
    await userEvent.click(within(confirm).getByRole("button", { name: "Change access" }));
    await waitFor(() => expect(server.sent("PUT", url)?.body).toMatchObject({ confirmVisibility: true }));
  });
});
