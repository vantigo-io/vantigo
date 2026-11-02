import type { components } from "../api-schema";

type Schemas = components["schemas"];

export type AssistantAvailability = Schemas["AssistantAvailability"];
export type AssistantConversation = Schemas["AssistantConversation"];
export type AssistantConversationDetail = Schemas["AssistantConversationDetail"];
export type AssistantConversationList = Schemas["AssistantConversationList"];
export type AssistantMessage = Schemas["AssistantMessage"];
export type AssistantMessageContext = Schemas["AssistantMessageContext"];
export type AssistantPart = Schemas["AssistantPart"];
export type AssistantRun = Schemas["AssistantRun"];
export type AssistantSettings = Schemas["AssistantSettings"];
export type AssistantMode = AssistantSettings["mode"];
export type AssistantPermission = Schemas["AssistantPermission"];
export type AssistantPermissionGroup = Schemas["AssistantPermissionGroup"];
export type AssistantPermissionLevel = Schemas["AssistantPermissionLevel"];
export type AssistantPermissions = Schemas["AssistantPermissions"];
export type AssistantPermissionResult = Schemas["AssistantPermissionResult"];
export type AssistantPerson = Schemas["AssistantPerson"];
export type AssistantMe = Schemas["AssistantMe"];
export type AssistantUpdateConversation = Schemas["AssistantUpdateConversationRequest"];
export type AssistantSettingsRequest = Schemas["AssistantSettingsRequest"];
export type AssistantPendingAction = Schemas["AssistantPendingAction"];
export type AssistantPendingField = Schemas["AssistantPendingField"];
export type AssistantUsage = Schemas["AssistantUsage"];
export type AssistantUsageRow = Schemas["AssistantUsageRow"];
