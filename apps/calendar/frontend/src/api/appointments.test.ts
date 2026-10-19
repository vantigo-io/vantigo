import { describe, expect, it } from "vitest";
import { appointmentFixture } from "../test/server";
import { appointmentWriteOf } from "./appointments";

describe("appointmentWriteOf", () => {
  it("puts the appointment back at a new span in the zone it was made in, not the mover's", () => {
    const appointment = appointmentFixture({
      timeZone: "Europe/Oslo",
      location: "Bergen",
      recurrence: { rule: "FREQ=WEEKLY;INTERVAL=1;BYDAY=TU" },
      customer: { id: 42, name: "Nordlys AS" },
      attendees: [{ id: 1, kind: "user", user: { id: "kari", displayName: "Kari" }, status: "accepted" }],
    });
    expect(
      appointmentWriteOf(appointment, {
        allDay: false,
        startsAt: "2026-10-20T08:00:00.000Z",
        endsAt: "2026-10-20T09:00:00.000Z",
      }),
    ).toEqual({
      title: "Kickoff with Nordlys AS",
      allDay: false,
      startsAt: "2026-10-20T08:00:00.000Z",
      endsAt: "2026-10-20T09:00:00.000Z",
      timeZone: "Europe/Oslo",
      visibility: "shared",
      transparency: "busy",
      location: "Bergen",
      recurrence: { rule: "FREQ=WEEKLY;INTERVAL=1;BYDAY=TU" },
      attendees: [{ kind: "user", userId: "kari" }],
      customerId: 42,
    });
  });
});
