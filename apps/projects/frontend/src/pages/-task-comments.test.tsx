import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { TaskComment } from "../api/tasks";
import { COMMENT_BODY_MAX } from "../lib/tasks";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { TaskComments } from "./-task-comments";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** The same date/time formatting the row uses, so an assertion does not hard-code a locale's rendering. */
const when = (iso: string) =>
  new Intl.DateTimeFormat("en-US", { dateStyle: "medium", timeStyle: "short" }).format(new Date(iso));

const ADA = "11111111-1111-1111-1111-111111111111";
const ALAN = "22222222-2222-2222-2222-222222222222";

const first: TaskComment = {
  id: 9,
  author: { userId: ADA, displayName: "Ada Lovelace", active: true },
  body: "Looks good to me",
  createdAt: "2026-01-03T10:00:00Z",
};

const second: TaskComment = { ...first, id: 10, body: "And one more" };

interface StubOptions {
  /** How many pages the API answers with; a POST may push it up by one. */
  totalPages?: number;
  /** Set when a post should make a page that was not there before. */
  pagesAfterPost?: number;
  read?: Response;
}

const stubComments = ({ totalPages = 1, pagesAfterPost, read }: StubOptions = {}) => {
  let pages = totalPages;
  return stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const page = Number(url.searchParams.get("page") ?? 1);
    if (url.pathname === "/api/v1/projects/tasks/12/comments") {
      if (init?.method === "POST") {
        if (pagesAfterPost !== undefined) pages = pagesAfterPost;
        return Promise.resolve(jsonResponse(201, second));
      }
      if (read) return Promise.resolve(read.clone());
      return Promise.resolve(
        jsonResponse(200, {
          data: pages === 0 ? [] : page === 1 ? [first] : [second],
          pagination: {
            page,
            pageSize: 20,
            totalCount: pages,
            totalPages: pages,
            hasNextPage: page < pages,
            hasPreviousPage: page > 1,
          },
        }),
      );
    }
    if (url.pathname.startsWith("/api/v1/projects/tasks/12/comments/")) {
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(200, { ...first, body: "Looks better" }));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });
};

const renderComments = (props: Partial<Parameters<typeof TaskComments>[0]> = {}, options?: StubOptions) => {
  const fetchMock = stubComments(options);
  const result = renderWithProviders(
    <TaskComments taskId={12} canContribute canManage={false} currentUserId={ADA} {...props} />,
  );
  return { fetchMock, ...result };
};

describe("TaskComments", () => {
  it("posts a comment", async () => {
    const { fetchMock } = renderComments();

    await screen.findByText("Looks good to me");
    await userEvent.type(screen.getByRole("textbox", { name: "Write a comment…" }), "One more thing");
    await userEvent.click(screen.getByRole("button", { name: "Post comment" }));

    await waitFor(() => {
      const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "POST") ?? [];
      expect(String(url)).toBe("/api/v1/projects/tasks/12/comments");
      expect(JSON.parse(String(init?.body))).toEqual({ body: "One more thing" });
    });
  });

  it("shows a comment posted onto a page that was not loaded yet", async () => {
    renderComments({}, { totalPages: 1, pagesAfterPost: 2 });

    await screen.findByText("Looks good to me");
    await userEvent.type(screen.getByRole("textbox", { name: "Write a comment…" }), "One more thing");
    await userEvent.click(screen.getByRole("button", { name: "Post comment" }));

    expect(await screen.findByText("And one more")).toBeInTheDocument();
    expect(screen.getByText("Looks good to me")).toBeInTheDocument();
  });

  it("loads the next page on demand", async () => {
    renderComments({}, { totalPages: 2 });

    await screen.findByText("Looks good to me");
    await userEvent.click(screen.getByRole("button", { name: "Load more" }));

    expect(await screen.findByText("And one more")).toBeInTheDocument();
    expect(screen.getByText("Looks good to me")).toBeInTheDocument();
  });

  it("refuses a comment longer than the contract allows", async () => {
    renderComments();

    await screen.findByText("Looks good to me");
    await userEvent.click(screen.getByRole("textbox", { name: "Write a comment…" }));
    await userEvent.paste("x".repeat(COMMENT_BODY_MAX + 1));

    expect(await screen.findByText("A comment is at most 4000 characters")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Post comment" })).toBeDisabled();
  });

  it("lets the author rewrite their own comment", async () => {
    const { fetchMock } = renderComments();
    const name = `Edit Ada Lovelace's comment from ${when(first.createdAt)}`;

    await screen.findByText("Looks good to me");
    await userEvent.click(screen.getByRole("button", { name }));
    const draft = screen.getByRole("textbox", { name });
    await userEvent.clear(draft);
    await userEvent.type(draft, "Looks better");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "PUT") ?? [];
      expect(String(url)).toBe("/api/v1/projects/tasks/12/comments/9");
      expect(JSON.parse(String(init?.body))).toEqual({ body: "Looks better" });
    });
  });

  it("asks before deleting a comment", async () => {
    const { fetchMock } = renderComments();

    await screen.findByText("Looks good to me");
    await userEvent.click(
      screen.getByRole("button", { name: `Delete Ada Lovelace's comment from ${when(first.createdAt)}` }),
    );

    const confirm = await screen.findByRole("dialog", { name: "Delete this comment?" });
    await userEvent.click(within(confirm).getByRole("button", { name: "Delete the comment" }));

    await waitFor(() => {
      const [url] = fetchMock.actualCalls.find(([, request]) => request?.method === "DELETE") ?? [];
      expect(String(url)).toBe("/api/v1/projects/tasks/12/comments/9");
    });
  });

  it("offers editing and deleting only to the author, and deleting to a manager", async () => {
    const editName = `Edit Ada Lovelace's comment from ${when(first.createdAt)}`;
    const deleteName = `Delete Ada Lovelace's comment from ${when(first.createdAt)}`;

    const own = renderComments();
    await screen.findByText("Looks good to me");
    expect(screen.getByRole("button", { name: editName })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: deleteName })).toBeInTheDocument();
    own.unmount();

    const other = renderComments({ currentUserId: ALAN });
    await screen.findByText("Looks good to me");
    expect(screen.queryByRole("button", { name: editName })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: deleteName })).not.toBeInTheDocument();
    other.unmount();

    renderComments({ currentUserId: ALAN, canManage: true });
    await screen.findByText("Looks good to me");
    expect(screen.queryByRole("button", { name: editName })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: deleteName })).toBeInTheDocument();
  });

  it("shows a viewer the conversation without a composer", async () => {
    renderComments({ canContribute: false });

    expect(await screen.findByText("Looks good to me")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Post comment" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Edit /i })).not.toBeInTheDocument();
  });

  // Two comments from the same author used to share one "Edit the comment" /
  // "Delete the comment" name apiece; naming each after who wrote it is not
  // enough on its own, so the row also carries when it was posted.
  it("names two comments from the same author differently, by when each was posted", async () => {
    const earlier = { ...first, id: 9, createdAt: "2026-01-03T09:00:00Z" };
    const later = { ...first, id: 10, createdAt: "2026-01-03T15:30:00Z" };
    stubFetch((input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/v1/projects/tasks/12/comments") {
        return Promise.resolve(
          jsonResponse(200, {
            data: [earlier, later],
            pagination: {
              page: 1,
              pageSize: 20,
              totalCount: 2,
              totalPages: 1,
              hasNextPage: false,
              hasPreviousPage: false,
            },
          }),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderWithProviders(<TaskComments taskId={12} canContribute canManage={false} currentUserId={ADA} />);

    expect(
      await screen.findByRole("button", { name: `Edit Ada Lovelace's comment from ${when(earlier.createdAt)}` }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: `Edit Ada Lovelace's comment from ${when(later.createdAt)}` }),
    ).toBeInTheDocument();
  });

  it("says when nobody has commented, and reports comments it could not read", async () => {
    const quiet = renderComments({}, { totalPages: 0 });
    expect(await screen.findByText("No comments yet.")).toBeInTheDocument();
    quiet.unmount();

    renderComments({}, { read: jsonResponse(500, { title: "Unavailable" }) });
    expect(await screen.findByText("Could not load the comments")).toBeInTheDocument();
  });
});
