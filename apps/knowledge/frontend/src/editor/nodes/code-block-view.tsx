import { NativeSelect } from "@mantine/core";
import type { ReactNodeViewProps } from "@tiptap/react";
import { NodeViewContent, NodeViewWrapper } from "@tiptap/react";
import { useI18n } from "@vantigo/frontend-shell";
import { codeLanguages } from "./code-languages";

export const CodeBlockView = ({ node, updateAttributes, editor }: ReactNodeViewProps) => {
  const { t } = useI18n("knowledge");
  const language = typeof node.attrs.language === "string" ? node.attrs.language : "";
  const known = codeLanguages.some((option) => option.id === language);
  return (
    <NodeViewWrapper style={{ position: "relative" }}>
      <div contentEditable={false} style={{ position: "absolute", top: 6, right: 6, zIndex: 1 }}>
        {editor.isEditable ? (
          <NativeSelect
            size="xs"
            aria-label={t("codeLanguage")}
            value={language}
            onChange={(event) => updateAttributes({ language: event.currentTarget.value || null })}
            data={[
              { value: "", label: t("codeAuto") },
              ...(language && !known ? [{ value: language, label: language }] : []),
              ...codeLanguages.map((option) => ({ value: option.id, label: option.name })),
            ]}
          />
        ) : language ? (
          <span style={{ fontSize: "var(--mantine-font-size-xs)", color: "var(--mantine-color-dimmed)" }}>
            {codeLanguages.find((option) => option.id === language)?.name ?? language}
          </span>
        ) : null}
      </div>
      <pre>
        <NodeViewContent<"code"> as="code" className={language ? `language-${language}` : undefined} />
      </pre>
    </NodeViewWrapper>
  );
};
