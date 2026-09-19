import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderWithProviders } from "../test/render";
import { BudgetBar } from "./budget-bar";

/** The formatter the bar itself uses, so no assertion hard-codes a locale's separators. */
const rawMoney = (amount: number, currency = "NOK") =>
  new Intl.NumberFormat("en-US", { style: "currency", currency }).format(amount);

/** The same amount with its non-breaking space flattened, the way textContent matchers read it. */
const money = (amount: number, currency = "NOK") => rawMoney(amount, currency).replace(/\u00a0/g, " ");

const hours = (approved: number, submitted: number, draft: number) => ({
  approved: { hours: approved },
  submitted: { hours: submitted },
  draft: { hours: draft },
});

describe("BudgetBar", () => {
  it("draws the three buckets against the budget and marks where the budget is", () => {
    renderWithProviders(<BudgetBar segments={hours(210, 62, 40)} basis="hours" budget={400} overBudget={false} />);

    const bar = screen.getByTestId("budget-bar");
    expect(within(bar).getByTestId("budget-bar-approved")).toHaveStyle({ width: "52.5%" });
    expect(within(bar).getByTestId("budget-bar-submitted")).toHaveStyle({ width: "15.5%" });
    expect(within(bar).getByTestId("budget-bar-draft")).toHaveStyle({ width: "10%" });
    expect(within(bar).getByTestId("budget-bar-marker")).toHaveStyle({ left: "100%" });
    expect(within(bar).queryByTestId("budget-bar-overflow")).not.toBeInTheDocument();
  });

  // The marker has to stay visible on a bar that has overrun, so the bar is
  // scaled by what was logged and the stretch past the marker is the overflow.
  it("keeps the marker inside an over-budget bar and paints what is past it red", () => {
    renderWithProviders(<BudgetBar segments={hours(300, 150, 50)} basis="hours" budget={400} overBudget={true} />);

    const bar = screen.getByTestId("budget-bar");
    expect(within(bar).getByTestId("budget-bar-marker")).toHaveStyle({ left: "80%" });
    expect(within(bar).getByTestId("budget-bar-overflow")).toHaveStyle({ left: "80%", width: "20%" });
    // The overflow is an overlay: it must not resize the buckets underneath it,
    // or the proportions a reader compares would change with the overrun.
    expect(within(bar).getByTestId("budget-bar-approved")).toHaveStyle({ width: "60%" });
    expect(within(bar).getByTestId("budget-bar-submitted")).toHaveStyle({ width: "30%" });
    expect(within(bar).getByTestId("budget-bar-draft")).toHaveStyle({ width: "10%" });
  });

  // A third of a bar is 33.33333333333333 %, which must not reach the DOM.
  it("trims the float noise off a three-way split", () => {
    renderWithProviders(<BudgetBar segments={hours(1, 1, 1)} overBudget={false} />);

    expect(screen.getByTestId("budget-bar-approved")).toHaveStyle({ width: "33.3333%" });
  });

  it("leaves a bucket with nothing in it out of the bar, and keeps it in the legend", () => {
    renderWithProviders(<BudgetBar segments={hours(210, 0, 0)} basis="hours" budget={400} overBudget={false} />);

    expect(screen.queryByTestId("budget-bar-submitted")).not.toBeInTheDocument();
    expect(screen.getByTestId("budget-legend-submitted")).toHaveTextContent("Submitted");
    expect(screen.getByTestId("budget-legend-submitted")).toHaveTextContent("0 h");
  });

  it("says the whole bar in one sentence for somebody who cannot see it", () => {
    renderWithProviders(<BudgetBar segments={hours(210, 62, 40)} basis="hours" budget={400} overBudget={false} />);

    expect(screen.getByRole("img")).toHaveAccessibleName(
      "312 h of 400 h used: 210 h approved, 62 h submitted, 40 h draft.",
    );
  });

  // The server decides over budget on the exact ratio; the bar never re-derives
  // it from a rounded percentage.
  it("says over budget in that sentence when the server says so", () => {
    renderWithProviders(<BudgetBar segments={hours(300, 150, 50)} basis="hours" budget={400} overBudget={true} />);

    expect(screen.getByRole("img")).toHaveAccessibleName(
      "500 h of 400 h used, over budget: 300 h approved, 150 h submitted, 50 h draft.",
    );
  });

  it("counts money when the budget is money, and writes both figures in the legend", () => {
    renderWithProviders(
      <BudgetBar
        segments={{
          approved: { hours: 210, amount: 240000 },
          submitted: { hours: 62, amount: 120000 },
          draft: { hours: 40, amount: 0 },
        }}
        basis="amount"
        budget={480000}
        currency="NOK"
        overBudget={false}
      />,
    );

    expect(screen.getByTestId("budget-bar-approved")).toHaveStyle({ width: "50%" });
    expect(screen.getByRole("img")).toHaveAccessibleName(
      `${rawMoney(360000)} of ${rawMoney(480000)} used: ${rawMoney(240000)} approved, ${rawMoney(120000)} submitted, ${rawMoney(0)} draft.`,
    );
    const legend = screen.getByTestId("budget-legend-approved");
    expect(legend).toHaveTextContent("210 h");
    expect(legend).toHaveTextContent(money(240000));
  });

  it("draws no marker at all when there is no budget to measure against", () => {
    renderWithProviders(<BudgetBar segments={hours(60, 30, 10)} overBudget={false} />);

    expect(screen.queryByTestId("budget-bar-marker")).not.toBeInTheDocument();
    expect(screen.getByTestId("budget-bar-approved")).toHaveStyle({ width: "60%" });
    expect(screen.getByRole("img")).toHaveAccessibleName("100 h logged: 60 h approved, 30 h submitted, 10 h draft.");
  });

  // A table row has no space for a legend, but a reader still needs the numbers.
  it("leaves the legend off the small variant and keeps the numbers in its label", () => {
    renderWithProviders(
      <BudgetBar segments={hours(210, 62, 40)} basis="hours" budget={400} overBudget={false} size="sm" />,
    );

    expect(screen.queryByTestId("budget-bar-legend")).not.toBeInTheDocument();
    expect(screen.getByRole("img")).toHaveAccessibleName(
      "312 h of 400 h used: 210 h approved, 62 h submitted, 40 h draft.",
    );
  });

  // Colour alone never carries meaning: the draft bucket is hatched.
  it("tells the draft bucket apart without relying on colour", () => {
    renderWithProviders(<BudgetBar segments={hours(210, 62, 40)} basis="hours" budget={400} overBudget={false} />);

    expect(screen.getByTestId("budget-bar-draft").getAttribute("style")).toContain("repeating-linear-gradient");
  });
});
