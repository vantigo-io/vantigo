import { ActionIcon, Group, Menu, SimpleGrid, UnstyledButton } from "@mantine/core";
import { IconAlertTriangle, IconBulb, IconInfoCircle, IconMoodEmpty, IconOctagon } from "@tabler/icons-react";
import type { ReactNodeViewProps } from "@tiptap/react";
import { NodeViewContent, NodeViewWrapper } from "@tiptap/react";
import { useI18n } from "@vantigo/frontend-shell";
import { type CalloutKind, calloutEmojis, calloutKinds, isCalloutKind } from "./callout-kinds";

const kindLabel = {
  info: "calloutInfo",
  tip: "calloutTip",
  warning: "calloutWarning",
  danger: "calloutDanger",
} as const;

const kindColor = { info: "blue", tip: "teal", warning: "yellow", danger: "red" } as const;

const KindIcon = ({ kind }: { kind: CalloutKind }) => {
  const Icon = { info: IconInfoCircle, tip: IconBulb, warning: IconAlertTriangle, danger: IconOctagon }[kind];
  return <Icon size={18} aria-hidden />;
};

export const CalloutView = ({ node, updateAttributes, editor }: ReactNodeViewProps) => {
  const { t } = useI18n("knowledge");
  const kind: CalloutKind = isCalloutKind(node.attrs.kind) ? node.attrs.kind : "info";
  const emoji = typeof node.attrs.emoji === "string" ? node.attrs.emoji : null;
  const color = kindColor[kind];
  const marker = emoji ? <span aria-hidden>{emoji}</span> : <KindIcon kind={kind} />;
  return (
    <NodeViewWrapper
      data-callout={kind}
      style={{
        display: "flex",
        gap: "var(--mantine-spacing-xs)",
        margin: "var(--mantine-spacing-sm) 0",
        padding: "var(--mantine-spacing-xs) var(--mantine-spacing-sm)",
        borderRadius: "var(--mantine-radius-md)",
        background: `var(--mantine-color-${color}-light)`,
        borderLeft: `3px solid var(--mantine-color-${color}-filled)`,
      }}
    >
      <div contentEditable={false} style={{ paddingTop: 4, color: `var(--mantine-color-${color}-light-color)` }}>
        {editor.isEditable ? (
          <Menu position="bottom-start" withinPortal>
            <Menu.Target>
              <ActionIcon
                variant="subtle"
                color={color}
                size="sm"
                aria-label={t("calloutStyle", { kind: t(kindLabel[kind]) })}
              >
                {marker}
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Label>{t("calloutKind")}</Menu.Label>
              {calloutKinds.map((option) => (
                <Menu.Item
                  key={option}
                  leftSection={<KindIcon kind={option} />}
                  color={option === kind ? kindColor[option] : undefined}
                  onClick={() => updateAttributes({ kind: option })}
                >
                  {t(kindLabel[option])}
                </Menu.Item>
              ))}
              <Menu.Divider />
              <Menu.Label>{t("calloutEmoji")}</Menu.Label>
              <SimpleGrid cols={6} spacing={2} px={4} pb={4}>
                {calloutEmojis.map((option) => (
                  <UnstyledButton
                    key={option}
                    aria-label={option}
                    aria-pressed={option === emoji}
                    onClick={() => updateAttributes({ emoji: option })}
                    style={{ fontSize: 18, textAlign: "center", borderRadius: 4 }}
                  >
                    {option}
                  </UnstyledButton>
                ))}
              </SimpleGrid>
              <Menu.Item leftSection={<IconMoodEmpty size={18} />} onClick={() => updateAttributes({ emoji: null })}>
                {t("calloutNoEmoji")}
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        ) : (
          <Group gap={0} aria-label={t(kindLabel[kind])}>
            {marker}
          </Group>
        )}
      </div>
      <NodeViewContent style={{ flex: 1, minWidth: 0 }} />
    </NodeViewWrapper>
  );
};
