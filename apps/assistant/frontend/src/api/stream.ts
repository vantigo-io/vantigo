import { appUrl } from "@vantigo/frontend-shell";
import type { AssistantMessage, AssistantMessageContext } from "./types";

/**
 * One event of the message stream, as the server writes it: `event: <name>`
 * and one line of JSON `data:`.
 */
export type StreamEvent =
  | { event: "run"; data: { runId: string } }
  | { event: "message"; data: AssistantMessage }
  | { event: "delta"; data: { text: string } }
  | { event: "tool"; data: { callId: string; name: string; label: string; state: "started" | "done" } }
  | { event: "done"; data: { state: "done" | "stopped" } }
  | { event: "error"; data: { code: string } };

const eventNames = new Set(["run", "message", "delta", "tool", "done", "error"]);

/**
 * Reads a server-sent-event byte stream a chunk at a time. A chunk may end in
 * the middle of an event (or of a multi-byte character, which the decoder
 * holds), so what is not yet a whole event stays in the buffer. Comments (a
 * line that starts with a colon, the server's keep-alive) and events this
 * client does not know are dropped.
 */
export const createSseParser = () => {
  const decoder = new TextDecoder();
  let buffer = "";

  const parseBlock = (block: string): StreamEvent | undefined => {
    let name = "message";
    const data: string[] = [];
    for (const line of block.split("\n")) {
      if (line === "" || line.startsWith(":")) continue;
      const colon = line.indexOf(":");
      const field = colon === -1 ? line : line.slice(0, colon);
      const value = colon === -1 ? "" : line.slice(colon + 1).replace(/^ /, "");
      if (field === "event") name = value;
      else if (field === "data") data.push(value);
    }
    if (data.length === 0 || !eventNames.has(name)) return undefined;
    try {
      return { event: name, data: JSON.parse(data.join("\n")) } as StreamEvent;
    } catch {
      return undefined;
    }
  };

  return {
    /** Feeds bytes (or text) and returns the events they completed. */
    feed(chunk: Uint8Array | string): StreamEvent[] {
      buffer += typeof chunk === "string" ? chunk : decoder.decode(chunk, { stream: true });
      buffer = buffer.replaceAll("\r\n", "\n").replaceAll("\r", "\n");
      const events: StreamEvent[] = [];
      let boundary = buffer.indexOf("\n\n");
      while (boundary !== -1) {
        const parsed = parseBlock(buffer.slice(0, boundary));
        if (parsed) events.push(parsed);
        buffer = buffer.slice(boundary + 2);
        boundary = buffer.indexOf("\n\n");
      }
      return events;
    },
  };
};

/** A refusal before the stream began: the status, and the problem's `code` when it has one. */
export class StreamRefusedError extends Error {
  readonly status: number;
  readonly code?: string;

  constructor(message: string, status: number, code?: string) {
    super(message);
    this.name = "StreamRefusedError";
    this.status = status;
    this.code = code;
  }
}

export interface SendMessageBody {
  text: string;
  context?: AssistantMessageContext;
}

/**
 * Sends a message and yields the run's events as they arrive. This is a
 * `fetch` with a `ReadableStream` reader over the POST, not `EventSource`:
 * the request has a body, and the session cookie must go with it. Aborting
 * the signal stops listening, not the run: the server finishes it either way.
 */
export async function* sendMessage(
  conversationId: string,
  body: SendMessageBody,
  signal?: AbortSignal,
): AsyncGenerator<StreamEvent, void, void> {
  const response = await fetch(appUrl(`/api/v1/assistant/conversations/${conversationId}/messages`), {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
    credentials: "include",
    body: JSON.stringify(body),
    signal,
  });
  if (!response.ok) {
    const problem = (await response.json().catch(() => null)) as {
      title?: string;
      detail?: string;
      code?: string;
    } | null;
    throw new StreamRefusedError(
      problem?.detail ?? problem?.title ?? `Request failed (HTTP ${response.status})`,
      response.status,
      problem?.code,
    );
  }
  if (!response.body) return;
  const reader = response.body.getReader();
  const parser = createSseParser();
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) return;
      for (const event of parser.feed(value)) yield event;
    }
  } finally {
    // A consumer that stops early (or an abort) leaves the body unread; release it.
    reader.cancel().catch(() => undefined);
  }
}
