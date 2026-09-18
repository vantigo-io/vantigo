import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { TaskFormModal } from "./-task-form-modal";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const stubCreate = (createResponse: Response) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/projects/7/tasks") {
      return Promise.resolve(init?.method === "POST" ? createResponse.clone() : jsonResponse(200, []));
    }
    if (url.pathname === "/api/v1/projects/7/roles") return Promise.resolve(jsonResponse(200, []));
    if (url.pathname === "/api/v1/projects/7/assignable-users") return Promise.resolve(jsonResponse(200, []));
    return Promise.resolve(new Response(null, { status: 404 }));
  });

const fillAndSubmit = async (title: string) => {
  // The field is `withAsterisk`, so its label reads "Title *".
  await userEvent.type(await screen.findByLabelText(/^Title/), title);
  await userEvent.click(screen.getByRole("button", { name: "Create" }));
};

describe("TaskFormModal", () => {
  it("creates a task on the project it was opened for", async () => {
    const fetchMock = stubCreate(jsonResponse(201, { id: 12, title: "Write the docs" }));
    renderWithProviders(<TaskFormModal projectId={7} state={{ mode: "create" }} onClose={() => {}} />);

    await fillAndSubmit("Write the docs");

    await waitFor(() => {
      const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "POST") ?? [];
      expect(String(url)).toBe("/api/v1/projects/7/tasks");
      expect(JSON.parse(String(init?.body))).toMatchObject({ title: "Write the docs", status: "todo" });
    });
  });

  // The host's "Create task" picker offers every project the caller works on,
  // and a viewer's role does not let them write its tasks — the backend answers
  // 403. The form must say so and stay open on what was typed, so the caller
  // can close it or pick another project, rather than swallowing the refusal.
  it("keeps the form open and reports the refusal when the project turns it down", async () => {
    stubCreate(jsonResponse(403, { title: "You may not contribute to this project" }));
    renderWithProviders(<TaskFormModal projectId={7} state={{ mode: "create" }} onClose={() => {}} />);

    await fillAndSubmit("Write the docs");

    expect(await screen.findByText("Could not save the task")).toBeInTheDocument();
    expect(await screen.findByText("You may not contribute to this project")).toBeInTheDocument();
    expect(screen.getByLabelText(/^Title/)).toHaveValue("Write the docs");
    expect(screen.getByRole("button", { name: "Create" })).toBeEnabled();
  });
});
