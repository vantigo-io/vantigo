import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { MyExpenses } from "./-my-expenses";
import "../../i18n";

// My expenses needs the caller's own id to narrow GET /entries, which widens
// for approvers, view-all, manage and a project manager. The host owns the
// session, so this wrapper reads it and hands it to the package's page — this
// test pins that hand-off rather than mocking the whole session machinery.
vi.mock("@vantigo/expenses-ui/pages/my-expenses", () => ({
  MyExpensesPage: ({ userId }: { userId: string }) => <div>expenses for {userId}</div>,
}));

const { fetchSession } = vi.hoisted(() => ({ fetchSession: vi.fn() }));
vi.mock("../../api/auth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/auth")>()),
  fetchSession,
}));

const renderRoute = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <MyExpenses />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("My expenses' entry point", () => {
  it("hands the page the signed-in user's own id", async () => {
    fetchSession.mockResolvedValue({
      user: { id: "3f1b0c2e-0000-4000-8000-000000000001", displayName: "Anna Ås", email: "anna@example.test" },
      isSystemAdmin: false,
    });

    renderRoute();

    expect(await screen.findByText("expenses for 3f1b0c2e-0000-4000-8000-000000000001")).toBeInTheDocument();
  });

  it("shows a skeleton rather than the page while the session has not resolved yet", () => {
    fetchSession.mockReturnValue(new Promise(() => {}));

    renderRoute();

    expect(screen.queryByText(/expenses for/)).not.toBeInTheDocument();
  });
});
