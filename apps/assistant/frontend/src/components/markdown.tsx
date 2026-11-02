import { Anchor, Code, Table, Typography } from "@mantine/core";
import type { ComponentPropsWithoutRef } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { classifyLink } from "../lib/links";

export interface MarkdownProps {
  children: string;
  /** Opens an in-app route; the host's router does it, so the panel stays open. */
  onNavigate?: (route: string) => void;
}

const plugins = [remarkGfm];

/**
 * An answer's Markdown, with GitHub's tables. Raw HTML is never rendered:
 * `react-markdown` turns it into text unless a plugin is added to parse it,
 * and none is. Images are dropped too (their alt text stays): a picture
 * fetched from an address the model chose is a way to carry data out. A link
 * opens only an in-app route or the instance's documentation; any other
 * address is shown as text with the address visible.
 */
export const Markdown = ({ children, onNavigate }: MarkdownProps) => {
  const components: Components = {
    a: ({ href, children: content }: ComponentPropsWithoutRef<"a">) => {
      const target = classifyLink(href);
      if (target.kind === "blocked")
        return (
          <span data-blocked-link>
            {content}
            {target.address && (
              <>
                {" "}
                <Code>{target.address}</Code>
              </>
            )}
          </span>
        );
      if (target.kind === "app")
        return (
          <Anchor
            href={target.route}
            onClick={(event) => {
              if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
              event.preventDefault();
              onNavigate?.(target.route);
            }}
          >
            {content}
          </Anchor>
        );
      return (
        <Anchor href={target.href} target="_blank" rel="noopener noreferrer">
          {content}
        </Anchor>
      );
    },
    table: ({ children: rows }: ComponentPropsWithoutRef<"table">) => (
      <Table.ScrollContainer minWidth={0}>
        <Table withTableBorder withColumnBorders fz="sm">
          {rows}
        </Table>
      </Table.ScrollContainer>
    ),
    thead: ({ children: rows }: ComponentPropsWithoutRef<"thead">) => <Table.Thead>{rows}</Table.Thead>,
    tbody: ({ children: rows }: ComponentPropsWithoutRef<"tbody">) => <Table.Tbody>{rows}</Table.Tbody>,
    tr: ({ children: cells }: ComponentPropsWithoutRef<"tr">) => <Table.Tr>{cells}</Table.Tr>,
    th: ({ children: content }: ComponentPropsWithoutRef<"th">) => <Table.Th>{content}</Table.Th>,
    td: ({ children: content }: ComponentPropsWithoutRef<"td">) => <Table.Td>{content}</Table.Td>,
  };
  return (
    <Typography fz="sm">
      <ReactMarkdown
        remarkPlugins={plugins}
        components={components}
        disallowedElements={["img"]}
        unwrapDisallowed
        // The library's own filter would drop a `tel:` or script address before the link rule sees it, and the
        // address is what the person is shown. Nothing is rendered as a link but what classifyLink passes.
        urlTransform={(url) => url}
      >
        {children}
      </ReactMarkdown>
    </Typography>
  );
};
