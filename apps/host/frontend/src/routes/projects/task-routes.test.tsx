import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MyTasksPage } from "./-my-tasks-page";
import { ProjectTasksTab } from "./-project-tasks-tab";
import "../../i18n";

const navigateMock = vi.hoisted(() => vi.fn());
const searchMock = vi.hoisted(() => vi.fn(() => ({}) as { task?: number; create?: true }));

// Three hand-offs the package's own tests cannot see, because they all arrive
// as props: the session user (whose comments may be edited), the task the URL
// deep-links to, and the navigation that keeps that URL in step.
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...actual,
    useParams: () => ({ projectId: 31 }),
    useSearch: () => searchMock(),
    useNavigate: () => navigateMock,
  };
});

vi.mock("@vantigo/projects-ui/pages/my-tasks", () => ({ MyTasksPage: () => <div>my tasks</div> }));

vi.mock("./-create-task-modal", () => ({
  CreateTaskModal: ({ opened, onClose }: { opened: boolean; onClose: () => void }) =>
    opened ? (
      <>
        <div>pick a project</div>
        <button type="button" onClick={onClose}>
          close the picker
        </button>
      </>
    ) : null,
}));

vi.mock("@vantigo/projects-ui/pages/project-tasks", () => ({
  ProjectTasks: ({
    projectId,
    currentUserId,
    openTaskId,
    onOpenTaskChange,
  }: {
    projectId: number;
    currentUserId?: string;
    openTaskId?: number;
    onOpenTaskChange?: (taskId: number | undefined) => void;
  }) => (
    <>
      <div>
        project {projectId} for {String(currentUserId)} on task {String(openTaskId)}
      </div>
      <button type="button" onClick={() => onOpenTaskChange?.(7)}>
        open 7
      </button>
      <button type="button" onClick={() => onOpenTaskChange?.(undefined)}>
        close
      </button>
    </>
  ),
}));

const renderTab = (task?: number) => {
  searchMock.mockReturnValue({ task });
  vi.mocked(useQuery).mockReturnValue({ data: { user: { id: "user-1", roles: [] } } } as never);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <ProjectTasksTab />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the project tasks tab route", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("hands the package the session user and the task the URL names", () => {
    renderTab(1042);

    expect(screen.getByText("project 31 for user-1 on task 1042")).toBeInTheDocument();
  });

  // Opening a task is somewhere to go back from; closing one consumes the
  // intent, so neither a refresh nor Back reopens a drawer just shut.
  it("pushes the opened task into the URL and replaces it away again on close", async () => {
    renderTab();

    await userEvent.click(screen.getByRole("button", { name: "open 7" }));
    expect(navigateMock).toHaveBeenCalledWith({
      to: "/projects/$projectId/tasks",
      params: { projectId: 31 },
      search: { task: 7 },
      replace: false,
    });

    await userEvent.click(screen.getByRole("button", { name: "close" }));
    expect(navigateMock).toHaveBeenCalledWith({
      to: "/projects/$projectId/tasks",
      params: { projectId: 31 },
      search: {},
      replace: true,
    });
  });
});

// My tasks is the package's page; the create intent Spotlight lands with is
// the host's, and it has to be consumed so a refresh or Back does not reopen
// the picker on a task the caller already created or abandoned.
describe("the my tasks route", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  const renderPage = (create?: true) => {
    searchMock.mockReturnValue({ create });
    render(
      <MantineProvider env="test">
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <MyTasksPage />
        </QueryClientProvider>
      </MantineProvider>,
    );
  };

  it("opens the project picker only when the URL asks for it", async () => {
    renderPage();
    expect(screen.queryByText("pick a project")).not.toBeInTheDocument();

    cleanup();
    renderPage(true);
    expect(await screen.findByText("pick a project")).toBeInTheDocument();
  });

  it("drops the create intent from the URL once the picker closes", async () => {
    renderPage(true);

    await userEvent.click(await screen.findByRole("button", { name: "close the picker" }));

    expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/my-tasks", search: {}, replace: true });
  });
});
