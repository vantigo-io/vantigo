import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { fakeServer, ids, jsonResponse } from "../test/api";
import { renderRoute } from "../test/render";

describe("the sidebar's search", () => {
  it("finds pages with their path and excerpt, and opens one", async () => {
    const server = fakeServer(({ method, url }) =>
      method === "GET" && url.startsWith("/api/v1/knowledge/search?")
        ? jsonResponse(200, {
            items: [
              {
                id: ids.onboarding,
                title: "Onboarding",
                icon: "👋",
                path: [{ id: ids.routines, title: "Routines" }],
                excerpt: [
                  { text: "Every new ", match: false },
                  { text: "employee", match: true },
                ],
                updatedAt: "2026-11-01T10:00:00Z",
              },
            ],
          })
        : undefined,
    );
    const { router } = renderRoute("/knowledge");
    await userEvent.type(await screen.findByRole("textbox", { name: "Search Knowledge" }), "employee");
    const results = await screen.findByRole("list", { name: "Search results" });
    expect(within(results).getByText("Onboarding")).toBeInTheDocument();
    expect(within(results).getByText("Routines")).toBeInTheDocument();
    expect(within(results).getByText("employee").tagName).toBe("MARK");
    expect(server.sent("GET", /\/search\?/)?.url).toBe("/api/v1/knowledge/search?q=employee&limit=20");
    await userEvent.click(within(results).getByRole("listitem"));
    await waitFor(() => expect(router.state.location.pathname).toBe(`/knowledge/articles/${ids.onboarding}`));
    expect(screen.queryByRole("list", { name: "Search results" })).toBeNull();
  });
});
