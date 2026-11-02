import "./i18n";

export { AssistantButton, type AssistantButtonProps } from "./components/assistant-button";
export { AssistantPanel, type AssistantPanelProps } from "./components/assistant-panel";
export type { PermissionsTranslation } from "./components/permissions-table";
export { AssistantPersonSwitch, type AssistantPersonSwitchProps } from "./components/person-switch";
export { AssistantSettingsCard, type AssistantSettingsCardProps } from "./components/settings-card";
export { UsageTable } from "./components/usage-table";
export { type UseAssistantShellOptions, useAssistantShell } from "./hooks/use-assistant-shell";
export { useAssistantAvailability } from "./hooks/use-availability";
export { assistantCatalog } from "./i18n";
export {
  type AssistantChip,
  type AssistantContextResolver,
  type AssistantRecordContext,
  type AssistantRouteInfo,
  assistantContext,
  createAssistantContextRegistry,
} from "./lib/context";
export { invalidateAfterAssistantWrites } from "./lib/invalidate";
export { type AssistantPanelController, type PendingQuestion, useAssistantPanel } from "./lib/panel";
export { useAssistantRunStatus } from "./lib/run-status";
export { isAssistantShortcut, shouldIgnoreAssistantShortcut, useAssistantShortcut } from "./lib/shortcut";
export { AssistantPage, type AssistantPageProps } from "./pages/assistant";
