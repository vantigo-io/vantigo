import { describe, expect, it } from "vitest";

import { actorLabel } from "./actor-label";

// The catalogue, reduced to what this function asks for: the key back, so a
// test failure names the key that was picked rather than its English text.
const t = (key: string) => `t:${key}`;

describe("actorLabel", () => {
  it("names a real person by the display the server snapshotted", () => {
    expect(actorLabel("user", "Anders Refsdal", t)).toBe("Anders Refsdal");
  });

  it("translates the server's sentinels rather than showing their English text", () => {
    // actorKind is what decides these, not the display: the server stores the
    // English literals at write time and a Norwegian reader must not see them.
    expect(actorLabel("system", "System", t)).toBe("t:actorSystem");
    expect(actorLabel("unattributed", "Unattributed", t)).toBe("t:unattributed");
    expect(actorLabel("user", "Unknown user", t)).toBe("t:actorUnknownUser");
  });

  it("falls back to unattributed when there is no name to show", () => {
    expect(actorLabel("user", null, t)).toBe("t:unattributed");
    expect(actorLabel("user", "   ", t)).toBe("t:unattributed");
    expect(actorLabel(undefined, undefined, t)).toBe("t:unattributed");
  });

  it("shows an unrecognised kind's display as given, rather than guessing", () => {
    // A kind this build does not know about is still an attribution the server
    // meant something by, so its display is shown verbatim.
    expect(actorLabel("service-account", "Nightly import", t)).toBe("Nightly import");
  });
});
