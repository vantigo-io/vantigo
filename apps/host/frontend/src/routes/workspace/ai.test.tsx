import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { AiSettings } from "../../api/ai";
import { stubFetch } from "../../test/fetch";
import { renderRoute } from "../../test/render-route";
import { AiModelsPage } from "./-ai-models";
import "../../i18n";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const refusal = (status: number, code: string) => json({ error: { code, message: `server text for ${code}` } }, status);

const task = (overrides: Partial<AiSettings["modules"][number]["tasks"][number]> = {}) => ({
  key: "communications.compose-reply",
  display: "Draft a reply",
  description: "Writes an editable reply to a customer.",
  model: "gpt-4o-mini",
  default: null,
  source: "plan" as const,
  version: "0",
  ...overrides,
});

/** GET /owner/ai/settings as an instance with a three-model plan answers it. */
const settings = (overrides: Partial<AiSettings> = {}): AiSettings => ({
  provider: "openai",
  allowedModels: ["gpt-4o-mini", "gpt-4o", "gpt-4.1"],
  planDefault: "gpt-4o-mini",
  keyConfigured: true,
  modules: [
    {
      module: "communications",
      tasks: [
        task(),
        task({
          key: "communications.customer-suggestion",
          display: "Suggest the customer",
          description: "Suggests which customer a conversation is from.",
          model: "gpt-4o",
          default: "gpt-4o",
          source: "default",
        }),
      ],
    },
    {
      module: "staff",
      tasks: [
        task({
          key: "staff.summarise",
          display: "Summarise a profile",
          description: "Summarises a person's profile.",
          model: "gpt-4.1",
          source: "chosen",
          version: "1767268800000000",
        }),
      ],
    },
  ],
  ...overrides,
});

type Handler = (method: string, url: string, init: RequestInit | undefined) => Response | undefined;

const settingsUrl = "/api/v1/identity/owner/ai/settings";

/** A fake identity API: GET settings answers `answers` in turn (the last one repeating), anything else goes to `handle`. */
const renderPage = async (answers: Array<AiSettings | Response> = [settings()], handle: Handler = () => undefined) => {
  let reads = 0;
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    if (method === "GET" && url === settingsUrl) {
      const answer = answers[Math.min(reads++, answers.length - 1)];
      return Promise.resolve(answer instanceof Response ? answer.clone() : json(answer));
    }
    return Promise.resolve(handle(method, url, init) ?? new Response(null, { status: 404 }));
  });
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await renderRoute("/workspace/ai", AiModelsPage, queryClient);
  return fetchMock;
};

const puts = (fetchMock: Awaited<ReturnType<typeof renderPage>>) =>
  fetchMock.mock.calls
    .map(([input, request]) => ({
      method: request?.method ?? "GET",
      url: String(input),
      headers: (request?.headers ?? {}) as Record<string, string>,
      body: typeof request?.body === "string" ? (JSON.parse(request.body) as Record<string, unknown>) : undefined,
    }))
    .filter((call) => call.method === "PUT");

/** The visible input of a task's select: the hidden one beside it carries the label too. */
const modelSelect = (name: string) =>
  screen
    .getAllByLabelText(`Model for ${name}`)
    .find((element) => element.tagName === "INPUT" && (element as HTMLInputElement).type !== "hidden") as HTMLElement;

describe("the AI models page", () => {
  it("states the plan as facts, never the key, and groups the tasks by module", async () => {
    await renderPage();

    expect(await screen.findByText("OpenAI")).toBeInTheDocument();
    expect(screen.getByText("AI models")).toBeInTheDocument();
    for (const model of ["gpt-4o-mini", "gpt-4o", "gpt-4.1"]) {
      expect(screen.getAllByText(model).length).toBeGreaterThan(0);
    }
    expect(screen.getByText("An API key is configured by Vantigo.")).toBeInTheDocument();
    expect(screen.queryByLabelText(/api key/i)).not.toBeInTheDocument();

    // Grouped by module, each under the module's own name where the host has one.
    expect(screen.getByRole("heading", { name: "Communications" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Staff" })).toBeInTheDocument();
    for (const name of ["Draft a reply", "Suggest the customer", "Summarise a profile"]) {
      expect(screen.getByText(name)).toBeInTheDocument();
      expect(modelSelect(name)).toBeEnabled();
    }
    expect(screen.getByText("Writes an editable reply to a customer.")).toBeInTheDocument();
    expect(modelSelect("Draft a reply")).toHaveValue("gpt-4o-mini");
    expect(modelSelect("Suggest the customer")).toHaveValue("gpt-4o");
    expect(modelSelect("Summarise a profile")).toHaveValue("gpt-4.1");
  });

  it("badges a task nobody has chosen for: Default for the module's recommendation, Plan default otherwise", async () => {
    await renderPage();
    await screen.findByText("Draft a reply");

    expect(screen.getAllByText("Plan default")).toHaveLength(1);
    expect(screen.getAllByText("Default")).toHaveLength(1);
  });

  it("saves a chosen model under If-Match on the task's version and shows the answer", async () => {
    const fetchMock = await renderPage([settings()], (method, url) =>
      method === "PUT" && url === "/api/v1/identity/owner/ai/tasks/communications.compose-reply"
        ? json(task({ model: "gpt-4.1", source: "chosen", version: "1767268801000000" }))
        : undefined,
    );
    await screen.findByText("Draft a reply");

    fireEvent.click(modelSelect("Draft a reply"));
    fireEvent.click(await screen.findByRole("option", { name: "gpt-4.1" }));

    await waitFor(() => expect(puts(fetchMock)).toHaveLength(1));
    const [put] = puts(fetchMock);
    expect(put.url).toBe("/api/v1/identity/owner/ai/tasks/communications.compose-reply");
    expect(put.headers["if-match"]).toBe("0");
    expect(put.body).toEqual({ model: "gpt-4.1" });
    expect(await screen.findByText("Model saved")).toBeInTheDocument();
    expect(screen.getByText("Draft a reply now uses gpt-4.1.")).toBeInTheDocument();
    await waitFor(() => expect(modelSelect("Draft a reply")).toHaveValue("gpt-4.1"));
    // A chosen task has no badge: one of the two is gone.
    expect(screen.queryByText("Plan default")).not.toBeInTheDocument();
  });

  it("reloads and says so when someone else changed the task first (412)", async () => {
    const changed = settings();
    changed.modules[0].tasks[0] = task({ model: "gpt-4o", source: "chosen", version: "1767268802000000" });
    const fetchMock = await renderPage([settings(), changed], (method) =>
      method === "PUT" ? refusal(412, "version_mismatch") : undefined,
    );
    await screen.findByText("Draft a reply");

    fireEvent.click(modelSelect("Draft a reply"));
    fireEvent.click(await screen.findByRole("option", { name: "gpt-4.1" }));

    expect(await screen.findByText("The AI models have changed")).toBeInTheDocument();
    await waitFor(() => expect(modelSelect("Draft a reply")).toHaveValue("gpt-4o"));
    expect(puts(fetchMock)).toHaveLength(1);
  });

  it("says what the server refused when the model cannot be saved", async () => {
    await renderPage([settings()], (method) => (method === "PUT" ? refusal(422, "model_not_allowed") : undefined));
    await screen.findByText("Draft a reply");

    fireEvent.click(modelSelect("Draft a reply"));
    fireEvent.click(await screen.findByRole("option", { name: "gpt-4.1" }));

    expect(await screen.findByText("The model could not be saved")).toBeInTheDocument();
    expect(screen.getByText("server text for model_not_allowed")).toBeInTheDocument();
    expect(modelSelect("Draft a reply")).toHaveValue("gpt-4o-mini");
  });

  it("locks every select, with the model shown, when the plan allows one model", async () => {
    const onlyOne = settings({ allowedModels: ["gpt-4o-mini"] });
    for (const group of onlyOne.modules) for (const one of group.tasks) one.model = "gpt-4o-mini";
    const fetchMock = await renderPage([onlyOne]);
    await screen.findByText("Draft a reply");

    expect(
      screen.getByText("The plan allows one model, so every task uses it and there is nothing to choose."),
    ).toBeInTheDocument();
    for (const name of ["Draft a reply", "Suggest the customer", "Summarise a profile"]) {
      expect(modelSelect(name)).toBeDisabled();
      expect(modelSelect(name)).toHaveValue("gpt-4o-mini");
    }
    expect(puts(fetchMock)).toHaveLength(0);
  });

  it("is the not-found page while the instance has no AI provider (404)", async () => {
    await renderPage([refusal(404, "ai_not_configured")]);

    expect(await screen.findByText(/page not found|not found/i)).toBeInTheDocument();
    expect(screen.queryByText("AI models")).not.toBeInTheDocument();
  });

  it("says the models could not be loaded when the settings fail otherwise", async () => {
    await renderPage([refusal(500, "server_error")]);

    expect(await screen.findByText("The AI models could not be loaded.")).toBeInTheDocument();
  });
});
