import type { components } from "../api-schema";
import { request } from "./request";

/**
 * The AI plan and the model per AI task (Integrations design T12, T13): the
 * provider, the models the plan allows and that a key is configured are facts
 * from the instance's environment, and each task's model is an Owner's choice.
 * The key is never answered.
 */
export type AiSettings = components["schemas"]["AiSettings"];
export type AiSettingsModule = components["schemas"]["AiSettingsModule"];
export type AiTask = components["schemas"]["AiSettingsTask"];

export const aiSettingsQueryKey = ["owner", "ai", "settings"] as const;

const base = "/api/v1/identity/owner/ai";

/** The settings; a 404 ai_not_configured while the instance has no AI provider, which hides the page. */
export const fetchAiSettings = () => request<AiSettings>(`${base}/settings`);

/** Chooses the model a task uses, against the version the task was read at; a stale one is a 412. */
export const updateAiTask = (key: string, model: string, version: string) =>
  request<AiTask>(`${base}/tasks/${encodeURIComponent(key)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json", "If-Match": version },
    body: JSON.stringify({ model }),
  });
