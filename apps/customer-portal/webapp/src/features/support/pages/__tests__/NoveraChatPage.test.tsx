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

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import NoveraChatPage from "@features/support/pages/NoveraChatPage";

const mockNavigate = vi.fn();
const mockUseLocation = vi.fn();
const mockUseParams = vi.fn();
const mockClassifyCase = vi.fn();

vi.mock("react-router", () => ({
  useNavigate: () => mockNavigate,
  useLocation: () => mockUseLocation(),
  useParams: () => mockUseParams(),
}));

vi.mock("@api/usePostProjectDeploymentsSearch", () => ({
  usePostProjectDeploymentsSearchAll: () => ({ data: [], isLoading: false }),
}));

vi.mock("@features/support/api/useGetConversationMessages", () => ({
  useGetConversationMessages: () => ({
    data: undefined,
    isLoading: false,
    fetchNextPage: vi.fn(),
    hasNextPage: false,
    isFetchingNextPage: false,
  }),
}));

vi.mock("@features/settings/api/useGetUserDetails", () => ({
  default: () => ({ data: { email: "dev@wso2.com" } }),
}));

vi.mock("@features/support/api/usePostCaseClassifications", () => ({
  usePostCaseClassifications: () => ({ mutateAsync: mockClassifyCase }),
}));

// usePostChatEscalation/usePostChatMessage both call @tanstack/react-query's
// useMutation internally, which needs a real QueryClientProvider -- mocked
// out here the same way usePostCaseClassifications is above, so this file
// doesn't need to wrap every render in a provider just to satisfy hooks the
// tests in this file don't otherwise exercise.
const mockPostChatEscalation = vi.fn();
const mockPostChatMessage = vi.fn();

vi.mock("@features/support/api/usePostChatEscalation", () => ({
  usePostChatEscalation: () => ({
    mutateAsync: mockPostChatEscalation,
    isPending: false,
  }),
}));

vi.mock("@features/support/api/usePostChatMessage", () => ({
  usePostChatMessage: () => ({
    mutateAsync: mockPostChatMessage,
    isPending: false,
  }),
}));

vi.mock("@features/support/hooks/useAllDeploymentProducts", () => ({
  useAllDeploymentProducts: () => ({ productsByDeploymentId: {}, isLoading: false }),
}));

vi.mock("@api/useGetProjectDetails", () => ({
  default: () => ({ data: { account: { id: "account-1" }, type: { id: "type-1", label: "Enterprise" } } }),
}));

const mockConnect = vi.fn().mockResolvedValue(undefined);
const mockSendUserMessage = vi.fn().mockResolvedValue(undefined);

vi.mock("@features/support/api/useChatWebSocket", () => ({
  useChatWebSocket: () => ({
    connect: mockConnect,
    sendUserMessage: mockSendUserMessage,
  }),
}));

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = (await importOriginal()) as object;
  return {
    ...actual,
    useQueryClient: () => ({
      invalidateQueries: vi.fn(),
      getQueriesData: vi.fn(() => []),
    }),
  };
});

vi.mock("@features/support/components/novera-ai-assistant/novera-chat-page/ChatHeader", () => ({
  default: ({
    onBack,
    onCreateCase,
  }: {
    onBack: () => void;
    onCreateCase: () => void;
  }) => (
    <>
      <button onClick={onBack}>Back Chat</button>
      <button onClick={onCreateCase}>Create Case</button>
    </>
  ),
}));

vi.mock("@features/support/components/novera-ai-assistant/novera-chat-page/ChatMessageList", () => ({
  default: () => <div>MessageList</div>,
}));

vi.mock("@features/support/components/novera-ai-assistant/novera-chat-page/ChatInput", () => ({
  default: () => <div>ChatInput</div>,
}));

vi.mock("@features/support/components/novera-ai-assistant/novera-chat-page/ChatSkeleton", () => ({
  default: () => <div>ChatSkeleton</div>,
}));

describe("NoveraChatPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockClassifyCase.mockResolvedValue({});
    mockUseParams.mockReturnValue({ projectId: "project-1", conversationId: undefined });
    mockUseLocation.mockReturnValue({
      state: {
        initialUserMessage: "Need help with 504",
        messages: [],
      },
    });
  });

  it("should render chat composition shell", () => {
    render(<NoveraChatPage />);
    expect(screen.getByText("MessageList")).toBeInTheDocument();
    expect(screen.getByText("ChatInput")).toBeInTheDocument();
  });

  it("should navigate back and create-case branches", async () => {
    render(<NoveraChatPage />);
    fireEvent.click(screen.getByText("Back Chat"));
    fireEvent.click(screen.getByText("Create Case"));

    expect(mockNavigate).toHaveBeenCalledWith(-1);
    // No conversationId is available in this test (no urlConversationId, no
    // conversationResponse, local-dev flag unset) -- create-case's
    // waitForConversationId() falls through to its real
    // CONVERSATION_ID_WAIT_MS (3s) timeout before resolving null and
    // navigating, so this needs a longer waitFor than the default 1s.
    await waitFor(
      () => {
        expect(mockNavigate).toHaveBeenCalledWith(
          "/projects/project-1/support/chat/create-case",
          expect.objectContaining({
            state: expect.objectContaining({ messages: expect.any(Array) }),
          }),
        );
      },
      { timeout: 4000 },
    );
  });

  // CUSTOMER_PORTAL_LOCAL_DEV_CLIENT_CONVERSATION_ID_ENABLED (see
  // portalConfig.ts / public/config.js): a local-development-only fallback
  // that fabricates a conversationId up front so "Chat with an Engineer"
  // can be exercised against entity-service running with DATA_SOURCE=postgres,
  // where entity-service never emits conversation_created (see
  // chat-persistence-mapping-plan.md). These tests pin down both sides: the
  // flag off/unset must leave today's production behavior byte-for-byte
  // unchanged, and the flag on must supply a stable id before the very
  // first WebSocket message and reuse it for the rest of the session.
  describe("local-dev conversationId fallback (CUSTOMER_PORTAL_LOCAL_DEV_CLIENT_CONVERSATION_ID_ENABLED)", () => {
    const UUID_RE =
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

    afterEach(() => {
      window.config = {
        ...window.config,
        CUSTOMER_PORTAL_LOCAL_DEV_CLIENT_CONVERSATION_ID_ENABLED: undefined,
      };
    });

    it("sends an empty conversationId on the first WebSocket message when the flag is unset (production behavior)", async () => {
      render(<NoveraChatPage />);

      await waitFor(() => {
        expect(mockSendUserMessage).toHaveBeenCalled();
      });

      expect(mockSendUserMessage).toHaveBeenCalledWith(
        expect.objectContaining({ conversationId: "" }),
      );
    });

    it("sends an empty conversationId on the first WebSocket message when the flag is explicitly false", async () => {
      window.config = {
        ...window.config,
        CUSTOMER_PORTAL_LOCAL_DEV_CLIENT_CONVERSATION_ID_ENABLED: false,
      };

      render(<NoveraChatPage />);

      await waitFor(() => {
        expect(mockSendUserMessage).toHaveBeenCalled();
      });

      expect(mockSendUserMessage).toHaveBeenCalledWith(
        expect.objectContaining({ conversationId: "" }),
      );
    });

    it("generates a client-side conversationId before the first WebSocket message, and reuses the same id for the rest of the session, when the flag is enabled", async () => {
      const randomUUIDSpy = vi.spyOn(crypto, "randomUUID");
      window.config = {
        ...window.config,
        CUSTOMER_PORTAL_LOCAL_DEV_CLIENT_CONVERSATION_ID_ENABLED: true,
      };

      render(<NoveraChatPage />);

      await waitFor(() => {
        expect(mockSendUserMessage).toHaveBeenCalled();
      });

      const firstCallArg = mockSendUserMessage.mock.calls[0]?.[0] as {
        conversationId: string;
      };
      expect(firstCallArg.conversationId).toMatch(UUID_RE);

      // Reused, not regenerated: create-case reads the same conversationId
      // state (see waitForConversationId/performClassification), so it must
      // resolve to the exact same value handed to the first WS message.
      fireEvent.click(screen.getByText("Create Case"));
      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith(
          "/projects/project-1/support/chat/create-case",
          expect.objectContaining({
            state: expect.objectContaining({
              conversationId: firstCallArg.conversationId,
            }),
          }),
        );
      });

      // Generated exactly once for the whole chat session -- never
      // regenerated on re-render or on a later message/action.
      expect(randomUUIDSpy).toHaveBeenCalledTimes(1);
    });
  });
});

