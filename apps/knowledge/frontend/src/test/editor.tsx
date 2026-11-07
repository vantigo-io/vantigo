import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { render, waitFor } from "@testing-library/react";
import type { Editor } from "@tiptap/core";
import { expect } from "vitest";
import type { KnowledgeDocument } from "../api/articles";
import "../i18n";
import { ArticleEditor } from "../editor/article-editor";
import { useArticleEditor } from "../editor/use-article-editor";

/**
 * Mounts the article's editor alone, as the page does, and hands the test
 * the TipTap editor so it can type through commands (jsdom cannot type into
 * a contenteditable the way a browser does). Under a router, as the chips
 * link to pages.
 */
export const renderEditor = async ({
  content = { type: "doc", content: [{ type: "paragraph" }] },
  editable = true,
  articleId = "a1",
  onUpdate,
}: {
  content?: KnowledgeDocument;
  editable?: boolean;
  articleId?: string;
  onUpdate?: (editor: Editor) => void;
} = {}) => {
  const ref: { editor: Editor | null } = { editor: null };
  const Harness = () => {
    const editor = useArticleEditor({ content, editable, articleId, onUpdate });
    ref.editor = editor;
    return <ArticleEditor editor={editor} editable={editable} label="Content" articleId={articleId} />;
  };
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const router = createRouter({
    routeTree: createRootRoute({ component: Harness }),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  const view = render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  await waitFor(() => expect(ref.editor).not.toBeNull());
  return { ...view, editor: ref.editor as unknown as Editor, queryClient, router };
};
