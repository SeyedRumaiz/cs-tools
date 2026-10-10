// Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

import { render, screen, fireEvent } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ConversationDetailsPage from "@features/support/pages/ConversationDetailsPage";

const mockNavigate = vi.fn();
const mockUseLocation = vi.fn();
const mockUseParams = vi.fn();
const mockUseGetConversationMessages = vi.fn();

vi.mock("react-router", () => ({
  useNavigate: () => mockNavigate,
  useLocation: () => mockUseLocation(),
  useParams: () => mockUseParams(),
}));

vi.mock("@features/support/api/useGetConversationMessages", () => ({
  useGetConversationMessages: () => mockUseGetConversationMessages(),
}));

vi.mock("@features/support/api/useChatWebSocket", () => ({
  useChatWebSocket: () => ({
    connect: vi.fn(),
    sendUserMessage: vi.fn(),
  }),
}));

vi.mock("@api/useGetProjectDetails", () => ({
  default: () => ({ data: { account: { id: "account-1" } } }),
}));

vi.mock("@features/settings/api/useGetUserDetails", () => ({
  default: () => ({ data: { email: "dev@wso2.com" } }),
}));

vi.mock(
  "@features/support/components/knowledge-base/ConversationKnowledgeRecommendations",
  () => ({
    default: () => <div>Knowledge</div>,
  }),
);

describe("ConversationDetailsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockUseParams.mockReturnValue({
      projectId: "project-1",
      conversationId: "conv-1",
    });
    mockUseLocation.mockReturnValue({
      state: {
        returnTo: "/projects/project-1/support/conversations",
        conversationSummary: {
          chatId: "conv-1",
          status: "Open",
          startedTime: "2026-05-01T00:00:00Z",
          messages: 2,
        },
      },
    });
    mockUseGetConversationMessages.mockReturnValue({
      data: { pages: [{ comments: [] }] },
      isLoading: false,
      isError: false,
      error: undefined,
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
    });
  });

  it("should render loading branch for conversation messages", () => {
    mockUseGetConversationMessages.mockReturnValue({
      data: undefined,
      isLoading: true,
      isError: false,
      error: undefined,
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
    });

    render(<ConversationDetailsPage />);

    expect(screen.getByText("Chat Session")).toBeInTheDocument();
    expect(screen.getByText("Conversation")).toBeInTheDocument();
  });

  it("hides Novera's <thinking> reasoning in a past conversation but not the user's own text", () => {
    const message = (id: string, createdBy: string, content: string) => ({
      id,
      createdBy,
      content,
      type: "comments",
      createdOn: `2026-05-01T00:00:0${id}Z`,
      isEscalated: false,
      hasInlineAttachments: false,
      inlineAttachments: [],
    });
    mockUseGetConversationMessages.mockReturnValue({
      data: {
        pages: [
          {
            comments: [
              message("1", "dev@wso2.com", "why do I see <thinking> here?"),
              message(
                "2",
                "Novera",
                "<thinking>The user asks about a product.\nI should ask a follow-up.</thinking>\n\nWhich environment is this?",
              ),
            ],
          },
        ],
      },
      isLoading: false,
      isError: false,
      error: undefined,
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
    });

    render(<ConversationDetailsPage />);

    expect(screen.getByText(/Which environment is this\?/)).toBeInTheDocument();
    expect(screen.queryByText(/follow-up/)).not.toBeInTheDocument();
    expect(screen.getByText(/why do I see <thinking> here\?/)).toBeInTheDocument();
  });

  // Regression: the real transcript stores a Novera reply with createdBy: ""
  // (never the literal name "Novera") and only whole-second precision, so it
  // often shares its triggering question's exact timestamp. Both the author
  // label ("Unknown" instead of "Novera") and the ordering (the reply
  // rendering above the question) were wrong before this fix.
  it("labels an empty-createdBy reply as Novera and keeps it after the question that shares its timestamp", () => {
    const message = (id: string, createdBy: string, content: string) => ({
      id,
      createdBy,
      content,
      type: "comment",
      createdOn: "2026-06-24T16:19:34Z",
      isEscalated: false,
      hasInlineAttachments: false,
      inlineAttachments: [],
    });
    mockUseGetConversationMessages.mockReturnValue({
      data: {
        pages: [
          {
            comments: [
              message("question-1", "Jane Doe", "hi i need help"),
              message("reply-1", "", "I'm sorry, you've reached your limit."),
            ],
          },
        ],
      },
      isLoading: false,
      isError: false,
      error: undefined,
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
    });

    render(<ConversationDetailsPage />);

    expect(screen.queryByText("Unknown")).not.toBeInTheDocument();
    const names = screen.getAllByText(/Jane Doe|Novera/);
    expect(names.map((el) => el.textContent)).toEqual([
      "Jane Doe",
      "Novera",
    ]);
  });

  it("should navigate to returnTo when back clicked", () => {
    render(<ConversationDetailsPage />);

    fireEvent.click(screen.getByText("Back"));

    expect(mockNavigate).toHaveBeenCalledWith(
      "/projects/project-1/support/conversations",
      { state: { fromBack: true } },
    );
  });

  it("strips [code] wrappers and recognizes empty createdBy as Novera bot with Markdown rendering", () => {
    mockUseGetConversationMessages.mockReturnValue({
      data: {
        pages: [
          {
            comments: [
              {
                id: "msg-user-1",
                createdBy: "Jane Doe",
                content: "[code]Asgardeo role adding drop down is not letting to add groups[/code]",
                type: "comment",
                createdOn: "2026-09-20T20:56:00Z",
                isEscalated: false,
                hasInlineAttachments: false,
                inlineAttachments: [],
              },
              {
                id: "msg-bot-1",
                createdBy: "",
                content:
                  "[code]I couldn't find a specific known bug. Clarify:\n1. **What exactly happens with the dropdown?**\n- Ensure groups have already been created under **User Management**[/code]",
                type: "comment",
                createdOn: "2026-09-20T20:56:01Z",
                isEscalated: false,
                hasInlineAttachments: false,
                inlineAttachments: [],
              },
            ],
          },
        ],
      },
      isLoading: false,
      isError: false,
      error: undefined,
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
    });

    render(<ConversationDetailsPage />);

    // Novera bot is recognized (not "Unknown")
    expect(screen.getByText("Novera")).toBeInTheDocument();
    expect(screen.queryByText("Unknown")).not.toBeInTheDocument();

    // Human user is recognized
    expect(screen.getByText("Jane Doe")).toBeInTheDocument();

    // [code] tags are stripped
    expect(screen.queryByText(/\[code\]/)).not.toBeInTheDocument();
    expect(screen.queryByText(/\[\/code\]/)).not.toBeInTheDocument();

    // Markdown rendered (bold text)
    expect(
      screen.getByText("What exactly happens with the dropdown?"),
    ).toBeInTheDocument();
    expect(screen.getByText("User Management")).toBeInTheDocument();
    expect(
      screen.getByText(
        "Asgardeo role adding drop down is not letting to add groups",
      ),
    ).toBeInTheDocument();
  });

  it("places human message before bot message when timestamps tie even if bot createdBy is empty", () => {
    mockUseGetConversationMessages.mockReturnValue({
      data: {
        pages: [
          {
            comments: [
              {
                id: "2-bot",
                createdBy: "",
                content: "[code]Hi! How can I help you today?[/code]",
                type: "comment",
                createdOn: "2026-10-05T10:00:51Z",
                isEscalated: false,
                hasInlineAttachments: false,
                inlineAttachments: [],
              },
              {
                id: "1-human",
                createdBy: "Alex Smith",
                content: "[code]hi[/code]",
                type: "comment",
                createdOn: "2026-10-05T10:00:51Z",
                isEscalated: false,
                hasInlineAttachments: false,
                inlineAttachments: [],
              },
            ],
          },
        ],
      },
      isLoading: false,
      isError: false,
      error: undefined,
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
    });

    render(<ConversationDetailsPage />);

    const userMsg = screen.getByText("hi");
    const botMsg = screen.getByText("Hi! How can I help you today?");

    // Check DOM order: user message must precede bot message
    expect(
      userMsg.compareDocumentPosition(botMsg) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("classifies message with explicit type bot as bot even if creator carries human names", () => {
    mockUseGetConversationMessages.mockReturnValue({
      data: {
        pages: [
          {
            comments: [
              {
                id: "bot-with-name",
                createdByFirstName: "AI",
                createdByLastName: "Assistant",
                content: "**Hello from bot**",
                type: "bot",
                createdOn: "2026-10-05T10:00:51Z",
                isEscalated: false,
                hasInlineAttachments: false,
                inlineAttachments: [],
              },
            ],
          },
        ],
      },
      isLoading: false,
      isError: false,
      error: undefined,
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
    });

    render(<ConversationDetailsPage />);

    expect(screen.getByText("Novera")).toBeInTheDocument();
    expect(screen.getByText("Hello from bot")).toBeInTheDocument();
  });

  it("strips case-insensitive and escaped code block tags", () => {
    mockUseGetConversationMessages.mockReturnValue({
      data: {
        pages: [
          {
            comments: [
              {
                id: "case-escaped-code",
                createdBy: "Jane Doe",
                content: "[CODE]Part 1[/CODE][\\code]Part 2[/code]",
                type: "comment",
                createdOn: "2026-10-05T10:00:51Z",
                isEscalated: false,
                hasInlineAttachments: false,
                inlineAttachments: [],
              },
            ],
          },
        ],
      },
      isLoading: false,
      isError: false,
      error: undefined,
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
    });

    render(<ConversationDetailsPage />);

    expect(screen.queryByText(/\[CODE\]/)).not.toBeInTheDocument();
    expect(screen.queryByText(/\[\\code\]/)).not.toBeInTheDocument();
    expect(screen.getByText(/Part 1/)).toBeInTheDocument();
    expect(screen.getByText(/Part 2/)).toBeInTheDocument();
  });

  it("preserves Customer comment added in message prose while removing leading label", () => {
    mockUseGetConversationMessages.mockReturnValue({
      data: {
        pages: [
          {
            comments: [
              {
                id: "prose-comment",
                createdBy: "Jane Doe",
                content:
                  "<p>Customer comment added</p>Please note: Customer comment added should not be stripped from prose.",
                type: "comment",
                createdOn: "2026-10-05T10:00:51Z",
                isEscalated: false,
                hasInlineAttachments: false,
                inlineAttachments: [],
              },
            ],
          },
        ],
      },
      isLoading: false,
      isError: false,
      error: undefined,
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
    });

    render(<ConversationDetailsPage />);

    expect(
      screen.getByText(
        "Please note: Customer comment added should not be stripped from prose.",
      ),
    ).toBeInTheDocument();
  });
});
