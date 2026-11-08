import type { components } from "../api-schema";

type Schemas = components["schemas"];

export type Agent = Schemas["Agent"];
export type AgentRole = Schemas["AgentRole"];
export type AgentTemplate = Schemas["AgentTemplate"];
export type AgentTemplateRule = Schemas["AgentTemplateRule"];
export type AgentSetup = Schemas["AgentSetup"];
export type AgentSetupItem = Schemas["AgentSetupItem"];
export type AgentsSettings = Schemas["AgentsSettings"];
export type AgentsSettingsRequest = Schemas["AgentsSettingsRequest"];
export type AgentCreateRequest = Schemas["AgentCreateRequest"];
export type AgentUpdateRequest = Schemas["AgentUpdateRequest"];
export type AgentRoleCreateRequest = Schemas["AgentRoleCreateRequest"];
export type AgentRoleUpdateRequest = Schemas["AgentRoleUpdateRequest"];

export type DisclosureWording = Agent["disclosureWording"];
export type AgentLanguages = Agent["languages"];
export type AgentState = Agent["state"];
export type PermissionLevel = AgentSetupItem["level"];
export type NeededLevel = AgentSetupItem["needed"];
export type ReplyMode = AgentRole["replyMode"];
export type HandOffWarning = AgentRole["handOffWarning"];
export type TemplateKey = AgentTemplate["key"];

/** A channel as Communications lists it; only what the role page reads, so no other app's package is imported. */
export interface ChannelOption {
  id: string;
  displayName: string | null;
  address: string;
  type: string;
  isActive: boolean;
}

/** A user Communications offers as an assignee; the hand-off is a person (V10). */
export interface AssignableUser {
  userId: string;
  displayName: string;
  kind?: "person" | "agent";
}
