import { MantineProvider } from "@mantine/core";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import "../i18n";
import { reviewItemIn } from "../lib/noteLinks";
import { NoteText } from "./NoteText";

const ID = "3f2c9a1e-5b7d-4c8e-9a10-2b3c4d5e6f70";

describe("an agent's note that something waits for review", () => {
  afterEach(cleanup);

  it("finds the item the note links", () => {
    expect(reviewItemIn(`Hanna has a reply waiting for review: /agents/review/${ID}`)).toEqual({
      before: "Hanna has a reply waiting for review",
      id: ID,
    });
    expect(reviewItemIn("Hanna handed this conversation to a colleague.")).toBeNull();
    expect(reviewItemIn("/agents/review/not-an-id")).toBeNull();
  });

  it("shows the link as Open in review", () => {
    render(
      <MantineProvider>
        <NoteText text={`Hanna has a reply waiting for review: /agents/review/${ID}`} />
      </MantineProvider>,
    );
    const link = screen.getByTestId("review-link");
    expect(link).toHaveTextContent("Open in review");
    expect(link.getAttribute("href")).toContain(`/agents/review/${ID}`);
    expect(screen.getByText(/Hanna has a reply waiting for review/)).toBeInTheDocument();
  });

  it("leaves any other note as written", () => {
    render(
      <MantineProvider>
        <NoteText text="Order SO-1042 shipped." />
      </MantineProvider>,
    );
    expect(screen.getByText("Order SO-1042 shipped.")).toBeInTheDocument();
    expect(screen.queryByTestId("review-link")).toBeNull();
  });
});
