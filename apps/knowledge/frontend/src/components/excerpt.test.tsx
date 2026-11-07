import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Excerpt } from "./excerpt";

describe("a search excerpt", () => {
  it("highlights the matched runs and shows everything else as text, tags included", () => {
    const { container } = render(
      <MantineProvider env="test">
        <Excerpt
          segments={[
            { text: "Send the ", match: false },
            { text: "invoice", match: true },
            { text: " <b>before</b> <script>alert(1)</script> Friday", match: false },
          ]}
        />
      </MantineProvider>,
    );
    expect(screen.getByText("invoice").tagName).toBe("MARK");
    expect(container.querySelector("script")).toBeNull();
    expect(container.querySelector("b")).toBeNull();
    expect(container.textContent).toContain("<b>before</b> <script>alert(1)</script> Friday");
    expect(container.querySelectorAll("mark")).toHaveLength(1);
  });
});
