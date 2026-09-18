import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CreateTaskModal } from "./-create-task-modal";
import "../../i18n";

// The package's task form is exercised by its own tests; what belongs to the
// host is picking the project it is opened on, so the form is reduced to that.
// Everything else in the package — the projects query this picker reads — is
// the real thing.
vi.mock("@vantigo/projects-ui", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@vantigo/projects-ui")>()),
  TaskFormModal: ({ projectId }: { projectId: number }) => <div>task form for {projectId}</div>,
}));

const project = (id: number, code: string, name: string) => ({
  id,
  code,
  name,
  status: "active",
  billingType: "time-and-materials",
  internal: false,
  managers: [],
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
});

const renderModal = (onClose = vi.fn()) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const tree = (opened: boolean) => (
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <CreateTaskModal opened={opened} onClose={onClose} />
      </QueryClientProvider>
    </MantineProvider>
  );
  const { rerender } = render(tree(true));
  // The route renders this from `?create=true`, so "the URL changed" is a
  // rerender with a different `opened`, not a call to onClose.
  return { onClose, setOpened: (opened: boolean) => rerender(tree(opened)) };
};

describe("the Create task quick action's project picker", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("asks for the caller's own projects and opens the task form on the one chosen", async () => {
    // `mine=true`: a task is created where the caller works, so the picker
    // offers their own projects rather than every project they may read. The
    // row only comes back for a request that asks for them.
    const fetchMock = vi.fn((input: RequestInfo | URL) =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            data: String(input).includes("mine=true") ? [project(31, "ACME1000", "Roof replacement")] : [],
            total: 1,
            page: 1,
            pageSize: 25,
            totalPages: 1,
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );
    vi.stubGlobal("fetch", fetchMock);
    renderModal();

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    await userEvent.click(screen.getByPlaceholderText("Search your projects"));
    await userEvent.click(await screen.findByText("ACME1000 — Roof replacement"));

    expect(await screen.findByText("task form for 31")).toBeInTheDocument();
    // The picker steps aside once it has answered its one question.
    expect(screen.queryByPlaceholderText("Search your projects")).not.toBeInTheDocument();
  });

  it("closes the task form when the intent leaves the URL, and forgets the project", async () => {
    vi.stubGlobal("fetch", () =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            data: [project(31, "ACME1000", "Roof replacement")],
            total: 1,
            page: 1,
            pageSize: 25,
            totalPages: 1,
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );
    const { setOpened } = renderModal();

    await userEvent.click(await screen.findByPlaceholderText("Search your projects"));
    await userEvent.click(await screen.findByText("ACME1000 — Roof replacement"));
    expect(await screen.findByText("task form for 31")).toBeInTheDocument();

    // Back drops `?create=true` without going through the close button.
    setOpened(false);
    await waitFor(() => expect(screen.queryByText("task form for 31")).not.toBeInTheDocument());

    // And the next Create task starts at the picker, not on the old project.
    setOpened(true);
    expect(await screen.findByPlaceholderText("Search your projects")).toBeInTheDocument();
    expect(screen.queryByText("task form for 31")).not.toBeInTheDocument();
  });

  it("says so when the caller has no projects to put a task on", async () => {
    vi.stubGlobal("fetch", () =>
      Promise.resolve(
        new Response(JSON.stringify({ data: [], total: 0, page: 1, pageSize: 25, totalPages: 1 }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );
    renderModal();

    await userEvent.click(await screen.findByPlaceholderText("Search your projects"));

    expect(await screen.findByText("No projects found")).toBeInTheDocument();
    expect(screen.queryByText(/task form for/)).not.toBeInTheDocument();
  });
});
