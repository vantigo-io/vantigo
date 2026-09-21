import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { CountrySelect } from "./country-select";

const renderSelect = (props: Partial<React.ComponentProps<typeof CountrySelect>> = {}) =>
  render(
    <MantineProvider env="test">
      <CountrySelect locale="en" label="Country" value={null} onChange={() => undefined} {...props} />
    </MantineProvider>,
  );

describe("CountrySelect", () => {
  it("renders as a searchable select with Norway pinned first", async () => {
    renderSelect();
    await userEvent.click(screen.getByRole("combobox", { name: "Country" }));
    const options = await screen.findAllByRole("option");
    expect(options[0]).toHaveTextContent("Norway");
  });

  it("shows the current value's localized name", () => {
    renderSelect({ value: "de" });
    expect(screen.getByRole("combobox", { name: "Country" })).toHaveValue("Germany");
  });
});
