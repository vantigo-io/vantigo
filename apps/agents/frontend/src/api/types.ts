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

export type ReviewItem = Schemas["ReviewItem"];
export type ReviewItemList = Schemas["ReviewItemList"];
export type ReviewCount = Schemas["ReviewCount"];
export type ReviewDiscardRequest = Schemas["ReviewDiscardRequest"];
export type ReviewSendRequest = Schemas["ReviewSendRequest"];
export type RoleCalibration = Schemas["RoleCalibration"];
export type CalibrationPoint = Schemas["CalibrationPoint"];
export type AgentUsage = Schemas["AgentUsage"];
export type AgentUsageDay = Schemas["AgentUsageDay"];
export type AgentRun = Schemas["AgentRun"];
export type AgentRunList = Schemas["AgentRunList"];
export type AgentRunDetail = Schemas["AgentRunDetail"];
export type AgentRunAction = Schemas["AgentRunAction"];
export type AgentRunMessage = Schemas["AgentRunMessage"];
export type AgentAuditList = Schemas["AgentAuditList"];
export type AgentAuditEvent = Schemas["AgentAuditEvent"];
export type AgentMemoryCounts = Schemas["AgentMemoryCounts"];
export type InstructionSuggestion = Schemas["InstructionSuggestion"];
export type InstructionSuggestionList = Schemas["InstructionSuggestionList"];

export type DisclosureWording = Agent["disclosureWording"];
export type AgentLanguages = Agent["languages"];
export type AgentState = Agent["state"];
export type PermissionLevel = AgentSetupItem["level"];
export type NeededLevel = AgentSetupItem["needed"];
export type ReplyMode = AgentRole["replyMode"];
export type HandOffWarning = AgentRole["handOffWarning"];
export type ScheduleKind = AgentRole["schedule"]["kind"];
export type TemplateKey = AgentTemplate["key"];
export type ReviewState = ReviewItem["state"];
export type DiscardReason = ReviewDiscardRequest["reason"];
export type RunState = AgentRun["state"];

/** A tag as Communications lists it; only what the role page reads. */
export interface TagOption {
  id: string;
  name: string;
}

/** What Communications answers about a hand-off (the contract's ConversationHandOff). */
export type ConversationHandOff = Schemas["ConversationHandOff"];

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
