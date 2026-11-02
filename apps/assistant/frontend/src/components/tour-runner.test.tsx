import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { type AssistTarget, setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AssistantMessage } from "../api/types";
import { endTour, startTour } from "../lib/tour";
import { renderWithProviders } from "../test/render";
import { assistantMessage } from "../test/server";
import { MessageList } from "./message-list";
import { TourRunner } from "./tour-runner";
import "../i18n";

const target = (key: string, route: string): AssistTarget => ({
  key,
  route,
  label: { en: key, nb: key },
  description: { en: key, nb: key },
});

const targets = [
  target("energy.metering-points.new", "/energy/metering-points"),
  target("energy.metering-points.form-save", "/energy/metering-points"),
  target("customers.customer.add-contact", "/customers/{customerId}"),
];

const steps = [
  { target: "energy.metering-points.new", title: "Start here", body: "Click New metering point." },
  { target: "energy.metering-points.form-save", title: "Then save", body: "Press Create metering point." },
];

/** The controls a screen would carry. */
const Screen = ({ keys }: { keys: string[] }) => (
  <div>
    {keys.map((key) => (
      <button key={key} type="button" data-assist={key}>
        {key}
      </button>
    ))}
  </div>
);

afterEach(() => {
  act(() => endTour());
  setLanguagePreference("auto");
});

describe("the tour runner", () => {
  it("does nothing until a tour is started", () => {
    renderWithProviders(
      <TourRunner targets={targets} onNavigate={() => {}} currentPath={() => "/energy/metering-points"} />,
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("points at the control, goes Next and Back, and closes", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <>
        <Screen keys={["energy.metering-points.new", "energy.metering-points.form-save"]} />
        <TourRunner targets={targets} onNavigate={() => {}} currentPath={() => "/energy/metering-points"} />
      </>,
    );
    act(() => startTour(steps));
    const first = await screen.findByRole("dialog");
    expect(within(first).getByText("Start here")).toBeInTheDocument();
    expect(first).not.toHaveAttribute("data-centered");

    await user.click(within(first).getByRole("button", { name: "Next" }));
    expect(await screen.findByText("Then save")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Back" }));
    expect(await screen.findByText("Start here")).toBeInTheDocument();

    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("navigates to the step's route first, then waits for the control to mount", async () => {
    const navigate = vi.fn();
    renderWithProviders(<TourRunner targets={targets} onNavigate={navigate} currentPath={() => "/customers"} />);
    act(() => startTour(steps));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith("/energy/metering-points"));
    // Nothing is shown while the control is not on screen.
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    // The page loads: the control appears, and the step with it.
    const button = document.createElement("button");
    button.setAttribute("data-assist", "energy.metering-points.new");
    act(() => {
      document.body.append(button);
    });
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Start here")).toBeInTheDocument();
    expect(dialog).not.toHaveAttribute("data-centered");
    button.remove();
  });

  it("shows a step centred, with its text, when its control never comes", async () => {
    renderWithProviders(
      <TourRunner targets={targets} onNavigate={() => {}} currentPath={() => "/energy/metering-points"} waitMs={30} />,
    );
    act(() => startTour(steps));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveAttribute("data-centered");
    expect(within(dialog).getByText("Start here")).toBeInTheDocument();
    expect(within(dialog).getByText("Click New metering point.")).toBeInTheDocument();
  });

  it("shows a step centred when its target is not in the registry, and navigates nowhere", async () => {
    const navigate = vi.fn();
    renderWithProviders(<TourRunner targets={[]} onNavigate={navigate} currentPath={() => "/anywhere"} />);
    act(() => startTour([{ target: "ghost.screen.control", title: "Ghost", body: "Not registered." }]));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveAttribute("data-centered");
    expect(navigate).not.toHaveBeenCalled();
  });

  it("does not navigate to a record's screen it cannot name, and shows the step centred", async () => {
    const navigate = vi.fn();
    renderWithProviders(
      <TourRunner targets={targets} onNavigate={navigate} currentPath={() => "/energy/metering-points"} waitMs={30} />,
    );
    act(() => startTour([{ target: "customers.customer.add-contact", title: "Add", body: "On a customer." }]));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveAttribute("data-centered");
    expect(navigate).not.toHaveBeenCalled();
  });

  it("speaks Norwegian bokmål in its labels", async () => {
    setLanguagePreference("nb");
    renderWithProviders(
      <>
        <Screen keys={["energy.metering-points.new", "energy.metering-points.form-save"]} />
        <TourRunner targets={targets} onNavigate={() => {}} currentPath={() => "/energy/metering-points"} />
      </>,
    );
    act(() => startTour(steps));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("button", { name: "Neste" })).toBeInTheDocument();
    expect(within(dialog).getByText("Steg 1 av 2")).toBeInTheDocument();
  });

  it("hands the focus back to the button that started it", async () => {
    const user = userEvent.setup();
    const parts: AssistantMessage = assistantMessage("Steps.", {
      id: "a1",
      parts: [
        { type: "text", text: "Steps." },
        { type: "tour", steps },
      ],
    });
    renderWithProviders(
      <>
        <Screen keys={["energy.metering-points.new", "energy.metering-points.form-save"]} />
        <MessageList messages={[parts]} partial="" working={false} stopped={false} />
        <TourRunner targets={targets} onNavigate={() => {}} currentPath={() => "/energy/metering-points"} />
      </>,
    );
    const show = screen.getByRole("button", { name: "Show me" });
    await user.click(show);
    await screen.findByRole("dialog");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(show).toHaveFocus();
  });
});

describe("the Show me button", () => {
  const tourMessage = assistantMessage("First the steps.", {
    id: "a1",
    parts: [
      { type: "text", text: "First the steps." },
      { type: "tour", steps },
    ],
  });

  it("is drawn under the answer and starts nothing by itself", () => {
    renderWithProviders(
      <>
        <MessageList messages={[tourMessage]} partial="" working={false} stopped={false} />
        <TourRunner targets={targets} onNavigate={() => {}} currentPath={() => "/energy/metering-points"} />
      </>,
    );
    expect(screen.getByText("First the steps.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Show me" })).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("is called Vis meg in Norwegian", () => {
    setLanguagePreference("nb");
    renderWithProviders(<MessageList messages={[tourMessage]} partial="" working={false} stopped={false} />);
    expect(screen.getByRole("button", { name: "Vis meg" })).toBeInTheDocument();
  });

  it("draws no button for a tour without steps", () => {
    const empty = assistantMessage("Nothing.", { id: "a2", parts: [{ type: "tour", steps: [] }] });
    renderWithProviders(<MessageList messages={[empty]} partial="" working={false} stopped={false} />);
    expect(screen.queryByRole("button", { name: "Show me" })).not.toBeInTheDocument();
  });

  it("starts the tour only when clicked", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <>
        <Screen keys={["energy.metering-points.new", "energy.metering-points.form-save"]} />
        <MessageList messages={[tourMessage]} partial="" working={false} stopped={false} />
        <TourRunner targets={targets} onNavigate={() => {}} currentPath={() => "/energy/metering-points"} />
      </>,
    );
    await user.click(screen.getByRole("button", { name: "Show me" }));
    expect(await screen.findByText("Start here")).toBeInTheDocument();
  });
});
