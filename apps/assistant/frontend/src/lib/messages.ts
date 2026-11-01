import type { AssistantMessage } from "../api/types";

/** What the person sees of a conversation: user and assistant messages. A tool-role message is the model's own business. */
export const visibleMessages = (messages: readonly AssistantMessage[]) =>
  messages.filter((message) => message.role !== "tool");

/** The words of a message: its text parts, joined. */
export const textOf = (message: AssistantMessage) =>
  message.parts
    .filter((part) => part.type === "text")
    .map((part) => part.text ?? part.content ?? "")
    .join("");
