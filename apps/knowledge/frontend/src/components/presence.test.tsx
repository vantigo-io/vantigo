import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import type { PresenceMember } from "@vantigo/frontend-shell";
import { describe, expect, it } from "vitest";
import "../i18n";
import { EditingBanner, PresenceAvatars } from "./presence";

const member = (name: string, state: PresenceMember["state"]): PresenceMember => ({
  userId: name,
  name,
  state,
  since: "2026-11-07T10:00:00Z",
});

const renderIt = (node: React.ReactNode) => render(<MantineProvider env="test">{node}</MantineProvider>);

describe("presence on an article", () => {
  it("says who is editing, and nothing while others only view", () => {
    const { rerender } = renderIt(<EditingBanner members={[member("Kari", "viewing")]} />);
    expect(screen.queryByRole("status")).toBeNull();
    rerender(
      <MantineProvider env="test">
        <EditingBanner members={[member("Kari", "editing"), member("Ola", "viewing")]} />
      </MantineProvider>,
    );
    expect(screen.getByRole("status")).toHaveTextContent("Kari is editing");
    rerender(
      <MantineProvider env="test">
        <EditingBanner members={[member("Kari", "editing"), member("Ola", "editing"), member("Per", "editing")]} />
      </MantineProvider>,
    );
    expect(screen.getByRole("status")).toHaveTextContent("Kari, Ola and Per are editing");
  });

  it("shows the others as avatars", () => {
    renderIt(<PresenceAvatars members={[member("Kari Nordmann", "viewing"), member("Ola Hansen", "editing")]} />);
    expect(screen.getByLabelText("Kari Nordmann")).toHaveTextContent("KN");
    expect(screen.getByLabelText("Ola Hansen")).toHaveTextContent("OH");
  });
});
