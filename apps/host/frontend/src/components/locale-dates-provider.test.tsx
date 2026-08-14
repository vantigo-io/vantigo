import { MantineProvider } from "@mantine/core";
import { useDatesContext } from "@mantine/dates";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MeteringPointDetailsPage } from "@vantigo/energy-ui";
import { I18nProvider, setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LocaleDatesProvider } from "./locale-dates-provider";

vi.mock("@tanstack/react-router", () => ({
  Link: "a",
  useParams: () => ({ meteringPointId: 1 }),
}));

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return {
    ...actual,
    useMutation: () => ({ isPending: false, mutate: vi.fn(), variables: undefined }),
    useQuery: () => ({ data: [] }),
    useQueryClient: () => ({ invalidateQueries: vi.fn() }),
    useSuspenseQuery: () => ({
      data: {
        id: 1,
        gsrn: "123456789012345678",
        meterNumber: null,
        address: { streetAddress: "Main Street 1", postalCode: "0001", city: "Oslo" },
        priceArea: "NO1",
        connectionStatus: "Connected",
        variants: [],
        effectivePrices: [],
        taxCategory: { name: "Standard", rate: 0.25 },
      },
    }),
  };
});

const LocaleProbe = () => <output>{useDatesContext().locale}</output>;

afterEach(() => {
  setLanguagePreference("auto");
});

describe("LocaleDatesProvider", () => {
  it("maps the active i18n locale to the Mantine DatesProvider locale", async () => {
    render(
      <I18nProvider preference="en">
        <LocaleDatesProvider>
          <LocaleProbe />
        </LocaleDatesProvider>
      </I18nProvider>,
    );

    expect(screen.getByText("en")).toBeInTheDocument();

    setLanguagePreference("nb");
    await waitFor(() => expect(screen.getByText("nb")).toBeInTheDocument());
  });

  it("mounts and updates a date control from a linked feature workspace", async () => {
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient()}>
          <I18nProvider preference="en">
            <LocaleDatesProvider>
              <MeteringPointDetailsPage />
            </LocaleDatesProvider>
          </I18nProvider>
        </QueryClientProvider>
      </MantineProvider>,
    );

    expect(screen.getByRole("textbox", { name: "From" })).toBeInTheDocument();

    setLanguagePreference("nb");
    const norwegianFromInput = await screen.findByRole("textbox", { name: "Fra" });
    fireEvent.click(norwegianFromInput);

    const norwegianCalendar = await waitFor(() => {
      const dropdown = Array.from(document.querySelectorAll<HTMLElement>("[data-dates-dropdown]")).find(
        (candidate) => candidate.style.display !== "none",
      );
      if (!dropdown) throw new Error("Date calendar did not open");
      return dropdown;
    });
    expect(norwegianCalendar.querySelector("[data-month-level]")?.textContent).toMatch(
      /januar|februar|mars|april|mai|juni|juli|august|september|oktober|november|desember/,
    );
    expect(norwegianCalendar).toHaveTextContent(/\b(ma|ti|on|to|fr|lø|sø)\b/);
    expect(norwegianCalendar.querySelector<HTMLElement>("[aria-label*='juli']")).toBeInTheDocument();

    fireEvent.keyDown(norwegianFromInput, { key: "Escape" });
    await waitFor(() =>
      expect(
        Array.from(document.querySelectorAll<HTMLElement>("[data-dates-dropdown]")).some(
          (candidate) => candidate.style.display !== "none",
        ),
      ).toBe(false),
    );
  });
});
