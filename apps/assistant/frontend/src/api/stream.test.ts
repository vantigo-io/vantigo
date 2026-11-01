import { describe, expect, it } from "vitest";
import { createSseParser } from "./stream";

const event = (name: string, data: unknown) => `event: ${name}\ndata: ${JSON.stringify(data)}\n\n`;

describe("createSseParser", () => {
  it("reads events that arrive whole", () => {
    const parser = createSseParser();
    const events = parser.feed(event("run", { runId: "r1" }) + event("delta", { text: "Hel" }));
    expect(events).toEqual([
      { event: "run", data: { runId: "r1" } },
      { event: "delta", data: { text: "Hel" } },
    ]);
  });

  it("holds a partial event until the rest arrives, wherever the chunk breaks", () => {
    const parser = createSseParser();
    const whole = event("delta", { text: "Hello" });
    const events = [];
    for (const ch of whole) events.push(...parser.feed(ch));
    expect(events).toEqual([{ event: "delta", data: { text: "Hello" } }]);
  });

  it("keeps a multi-byte character that is split between chunks", () => {
    const parser = createSseParser();
    const bytes = new TextEncoder().encode(event("delta", { text: "Målepunkt æøå" }));
    const cut = bytes.indexOf(0xc3) + 1;
    expect([...parser.feed(bytes.slice(0, cut)), ...parser.feed(bytes.slice(cut))]).toEqual([
      { event: "delta", data: { text: "Målepunkt æøå" } },
    ]);
  });

  it("reads CRLF line ends, joins data lines and drops comments and unknown events", () => {
    const parser = createSseParser();
    const text =
      ": keep-alive\r\n\r\n" +
      'event: tool\r\ndata: {"callId":"c1",\r\ndata: "name":"search_docs","label":"Searching","state":"started"}\r\n\r\n' +
      'event: nonsense\r\ndata: {"x":1}\r\n\r\n' +
      "event: delta\ndata: not json\n\n";
    expect(parser.feed(text)).toEqual([
      { event: "tool", data: { callId: "c1", name: "search_docs", label: "Searching", state: "started" } },
    ]);
  });
});
