import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { UserAvatar } from "./user-avatar";

const wrap = (ui: React.ReactNode) => render(<MantineProvider env="test">{ui}</MantineProvider>);

describe("UserAvatar", () => {
  it("puts the AI badge on an agent's avatar and says so in its label", () => {
    wrap(<UserAvatar name="Hanna" kind="agent" />);
    expect(screen.getByTestId("ai-badge")).toHaveTextContent("AI");
    expect(screen.getByLabelText("Hanna (AI)")).toBeInTheDocument();
  });

  it("is a plain avatar for a person", () => {
    wrap(<UserAvatar name="Kari Nordmann" kind="person" aria-label="Kari" />);
    expect(screen.queryByTestId("ai-badge")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Kari")).toBeInTheDocument();
  });

  it("is a plain avatar when the kind is not known", () => {
    wrap(<UserAvatar name="Kari Nordmann" />);
    expect(screen.queryByTestId("ai-badge")).not.toBeInTheDocument();
  });
});
