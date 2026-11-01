import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { type StubbedFetch, stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { SaveAsTemplateModal } from "./-save-as-template-modal";

const jsonResponse = (status: number, body: unknown, contentType = "application/json") =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": contentType } });

const saved = {
  id: 9,
  name: "Website plan",
  description: null,
  billingType: "time-and-materials",
  milestones: [],
  tasks: [],
  dependencies: [],
  revision: 1,
  createdAt: "2026-11-01T00:00:00Z",
  updatedAt: "2026-11-01T00:00:00Z",
};

const renderModal = () => {
  const onClose = vi.fn();
  renderWithProviders(<SaveAsTemplateModal projectId={7} projectName="Website" opened onClose={onClose} />);
  return { onClose };
};

const sentBody = (fetchMock: StubbedFetch) => JSON.parse(String(fetchMock.calls.at(-1)?.[1]?.body));

describe("SaveAsTemplateModal", () => {
  it("starts from the project's name and saves the template, pointing at the catalog", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(201, saved)));
    const { onClose } = renderModal();

    const name = await screen.findByLabelText(/^Name/);
    expect(name).toHaveValue("Website");
    await userEvent.type(name, " plan ");
    await userEvent.type(screen.getByLabelText(/^Description/), "  From the rollout  ");
    await userEvent.click(screen.getByRole("button", { name: "Save template" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock.calls.at(-1)?.[0]).toBe("/api/v1/projects/7/save-as-template");
    expect(fetchMock.calls.at(-1)?.[1]?.method).toBe("POST");
    expect(sentBody(fetchMock)).toEqual({ name: "Website plan", description: "From the rollout" });
    expect(await screen.findByText("Template Website plan created")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open Project templates" })).toHaveAttribute("href", "/projects/templates");
  });

  it("refuses a blank name before sending anything", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(201, saved)));
    renderModal();

    await userEvent.clear(await screen.findByLabelText(/^Name/));
    await userEvent.click(screen.getByRole("button", { name: "Save template" }));

    expect(await screen.findByText("A template name is required")).toBeInTheDocument();
    expect(fetchMock.calls).toHaveLength(0);
  });

  it("shows the server's name error next to the field", async () => {
    stubFetch(() =>
      Promise.resolve(
        jsonResponse(
          400,
          { title: "Invalid template", status: 400, errors: { name: ["Taken by the server"] } },
          "application/problem+json",
        ),
      ),
    );
    const { onClose } = renderModal();
    await userEvent.click(await screen.findByRole("button", { name: "Save template" }));

    expect(await screen.findByText("Taken by the server")).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("says when the project's plan breaks a template rule", async () => {
    stubFetch(() =>
      Promise.resolve(
        jsonResponse(
          400,
          {
            title: "Invalid template",
            status: 400,
            errors: { "tasks[0].checklist": ["A checklist holds at most 100 items."] },
          },
          "application/problem+json",
        ),
      ),
    );
    renderModal();
    await userEvent.click(await screen.findByRole("button", { name: "Save template" }));

    expect(
      await screen.findByText("The project's plan breaks a template rule: A checklist holds at most 100 items."),
    ).toBeInTheDocument();
  });
});
