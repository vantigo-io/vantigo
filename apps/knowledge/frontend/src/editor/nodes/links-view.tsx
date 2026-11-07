import { Anchor, Text } from "@mantine/core";
import { IconExternalLink, IconFileText, IconLock, IconTrashX } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import type { ReactNodeViewProps } from "@tiptap/react";
import { NodeViewWrapper } from "@tiptap/react";
import { useI18n } from "@vantigo/frontend-shell";
import type { CSSProperties } from "react";
import { resolvedLinkQueryOptions } from "../../api/files";
import { ARTICLE_ROUTE_PATH } from "../../lib/routes";

const chip: CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  gap: 4,
  padding: "0 6px",
  borderRadius: "var(--mantine-radius-sm)",
  background: "var(--mantine-color-default-hover)",
  verticalAlign: "baseline",
  lineHeight: 1.6,
};

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export const ArticleLinkView = ({ node }: ReactNodeViewProps) => {
  const { t } = useI18n("knowledge");
  const id = String(node.attrs.id ?? "");
  // Never the stored label (K21): it could name a page the reader may not read.
  const link = useQuery({ ...resolvedLinkQueryOptions(id), enabled: uuid.test(id) });
  const state = link.data?.state;
  return (
    <NodeViewWrapper as="span" data-type="articleLink" data-state={state ?? "loading"} style={{ display: "inline" }}>
      {state === "no_access" || state === "deleted" ? (
        <Text component="span" size="sm" c="dimmed" style={chip}>
          {state === "no_access" ? <IconLock size={14} aria-hidden /> : <IconTrashX size={14} aria-hidden />}
          <span style={state === "deleted" ? { textDecoration: "line-through" } : undefined}>
            {t(state === "no_access" ? "linkNoAccess" : "linkDeleted")}
          </span>
        </Text>
      ) : (
        <Anchor
          size="sm"
          style={chip}
          contentEditable={false}
          renderRoot={(props) => <Link to={ARTICLE_ROUTE_PATH} params={{ articleId: id }} {...props} />}
        >
          {link.data?.icon ? <span aria-hidden>{link.data.icon}</span> : <IconFileText size={14} aria-hidden />}
          {state === "ok" ? link.data?.title || t("untitled") : t("linkLoading")}
        </Anchor>
      )}
    </NodeViewWrapper>
  );
};

export const RecordLinkView = ({ node }: ReactNodeViewProps) => {
  const path = String(node.attrs.path ?? "");
  const label = (node.attrs.label as string | null) || path;
  return (
    <NodeViewWrapper as="span" data-type="recordLink" style={{ display: "inline" }}>
      <Anchor size="sm" style={chip} contentEditable={false} renderRoot={(props) => <Link to={path} {...props} />}>
        <IconExternalLink size={14} aria-hidden />
        {label}
      </Anchor>
    </NodeViewWrapper>
  );
};
