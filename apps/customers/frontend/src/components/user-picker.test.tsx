import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { UserPicker } from "./user-picker";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

const renderPicker = (props: Partial<Parameters<typeof UserPicker>[0]> = {}) => {
  stubFetch(
    vi.fn(() => Promise.resolve(new Response(JSON.stringify([]), { headers: { "Content-Type": "application/json" } }))),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <UserPicker
          label="Assignee"
          placeholder="Search"
          value={null}
          onChange={() => {}}
          clearLabel="Clear assignee"
          {...props}
        />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("UserPicker", () => {
  it("is a combobox under the label it was given, not a fixed one", () => {
    renderPicker();
    expect(screen.getByRole("combobox", { name: "Assignee" })).toBeInTheDocument();
  });

  // The rule OwnerPicker was built around and UserPicker inherits: the API
  // answers ACTIVE users only, and a person disabled after being chosen keeps
  // what they were given — so the current value has to survive a search that
  // does not contain it.
  it("keeps the current selection on the list whatever the search returned", () => {
    renderPicker({ value: "u9", selected: { userId: "u9", displayName: "Disabled Personsen" } });
    expect(screen.getByRole("combobox", { name: "Assignee" })).toHaveValue("Disabled Personsen");
  });
});
