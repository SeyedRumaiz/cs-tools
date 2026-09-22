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

import { Box, Paper, Divider } from "@wso2/oxygen-ui";
import {
  useState,
  useRef,
  useEffect,
  useCallback,
  useMemo,
  type JSX,
} from "react";
import { flushSync } from "react-dom";
import { useNavigate, useLocation } from "react-router";
import useNormalizedIdParam from "@hooks/useNormalizedIdParam";
import { usePostProjectDeploymentsSearchAll } from "@api/usePostProjectDeploymentsSearch";
import { useGetConversationMessages } from "@features/support/api/useGetConversationMessages";
import useGetUserDetails from "@features/settings/api/useGetUserDetails";
import { usePostCaseClassifications } from "@features/support/api/usePostCaseClassifications";
import { useChatWebSocket } from "@features/support/api/useChatWebSocket";
import {
  usePostChatEscalation,
  type EscalationPriorMessage,
} from "@features/support/api/usePostChatEscalation";
import {
  usePostChatMessage,
  type ChatMessageApiError,
} from "@features/support/api/usePostChatMessage";
import useGetProjectDetails from "@api/useGetProjectDetails";
import { useQueryClient, type InfiniteData } from "@tanstack/react-query";
import type { SearchProjectsResponse } from "@features/project-hub/types/projects";
import { ApiQueryKeys } from "@constants/apiConstants";
import type {
  SlotState,
  NoveraAction,
} from "@features/support/types/conversations";
import { NoveraActionType } from "@features/support/types/conversations";
import { useAllDeploymentProducts } from "@features/support/hooks/useAllDeploymentProducts";
import {
  DEFAULT_CONVERSATION_REGION,
  DEFAULT_CONVERSATION_TIER,
} from "@features/support/constants/conversationConstants";
import {
  CHAT_TYPING_CHARS_PER_TICK,
  CHAT_TYPING_INTERVAL_MS,
  HUMAN_CHAT_WS_HEARTBEAT_INTERVAL_MS,
  NOVERA_ANALYZING_PLACEHOLDER_TEXT,
  NOVERA_INITIAL_WELCOME_TEXT,
  NOVERA_WELCOME_MESSAGE_ID,
} from "@features/support/constants/chatConstants";
import {
  formatChatHistoryForClassification,
  buildEnvProducts,
} from "@features/support/utils/caseCreation";
import { filterDeploymentsForCaseCreation } from "@utils/permission";
import { htmlToPlainText } from "@features/support/utils/richTextEditor";
import { usePiiGuard } from "@features/support/hooks/usePiiGuard";
import PiiWarningDialog from "@features/support/components/dialogs/PiiWarningDialog";
import { ChatSender } from "@features/support/types/conversations";
import { MAX_FEEDBACK_TAGS } from "@features/support/constants/feedbackTags";
import type {
  ChatNavState,
  Message,
} from "@features/support/types/conversations";
import ChatHeader from "@features/support/components/novera-ai-assistant/novera-chat-page/ChatHeader";
import ChatInput from "@features/support/components/novera-ai-assistant/novera-chat-page/ChatInput";
import ChatMessageList from "@features/support/components/novera-ai-assistant/novera-chat-page/ChatMessageList";
import TokenRequestModal from "@features/support/components/novera-ai-assistant/novera-chat-page/TokenRequestModal";
import ChatSkeleton from "@features/support/components/novera-ai-assistant/novera-chat-page/ChatSkeleton";
import {
  buildEscalationPriorMessages,
  displayTextFromConversationContent,
  getFinalMessageFromPayload,
  sanitizeStreamToken,
  splitTokenForTyping,
} from "@features/support/utils/chat";
import {
  compareByCreatedOnThenId,
  dateFromApiCreatedOn,
} from "@features/support/utils/support";

// Max time (ms) to wait for the conversation id (delivered asynchronously over
// the chat WebSocket) before creating a case, so the created case links to the
// chat. Bounded so a missing id (e.g. socket down) can never block case creation.
const CONVERSATION_ID_WAIT_MS = 3000;

// Minimum number of messages the customer must send to Novera before
// "Chat with an Engineer" is offered — gives the AI assistant a real chance
// to resolve the issue first. See userMessageCount/liveEscalationEnabled
// below, where this is combined with the feature flag and conversationId.
const MIN_CUSTOMER_MESSAGES_BEFORE_ESCALATION = 3;

/**
 * NoveraChatPage component to provide AI-powered support assistance.
 *
 * @returns {JSX.Element} The rendered NoveraChatPage.
 */
export default function NoveraChatPage(): JSX.Element {
  const navigate = useNavigate();
  const projectId = useNormalizedIdParam("projectId");
  // Absent on the index route (/support/chat), which starts a new conversation.
  const urlConversationId = useNormalizedIdParam("conversationId");
  const location = useLocation();
  const navState = location.state as ChatNavState | null;
  const initialUserMessage = navState?.initialUserMessage;
  const conversationResponse = navState?.conversationResponse;
  const preloadedMessages = navState?.messages;
  const navAccountId = navState?.accountId;
  const chatNumber = navState?.chatNumber;
  const { data: userDetails } = useGetUserDetails();
  const currentUserEmail = userDetails?.email?.toLowerCase() ?? "";

  const handleBack = () => {
    navigate(-1);
  };

  const {
    data: allProjectDeployments,
    isLoading: isDeploymentsLoading,
  } = usePostProjectDeploymentsSearchAll(projectId || "");
  const { data: projectDetails } = useGetProjectDetails(projectId || "");
  const queryClient = useQueryClient();
  const projectTypeId = useMemo(() => {
    const cached = queryClient.getQueriesData<InfiniteData<SearchProjectsResponse>>({
      queryKey: [ApiQueryKeys.PROJECTS, "infinite"],
    });
    for (const [, data] of cached) {
      if (!data) continue;
      for (const page of data.pages) {
        const match = page.projects.find((p) => p.id === projectId);
        if (match?.type?.id) return match.type.id;
      }
    }
    return projectDetails?.type?.id ?? "";
  }, [queryClient, projectId, projectDetails?.type?.id]);
  const projectDeployments = useMemo(
    () =>
      filterDeploymentsForCaseCreation(
        allProjectDeployments,
        projectDetails?.type?.label,
      ),
    [allProjectDeployments, projectDetails?.type?.label],
  );
  const { productsByDeploymentId, isLoading: isProductsLoading } =
    useAllDeploymentProducts(projectDeployments);
  const isAllProductsLoading = isDeploymentsLoading || isProductsLoading;
  const envProducts = useMemo(
    () => buildEnvProducts(productsByDeploymentId, projectDeployments),
    [productsByDeploymentId, projectDeployments],
  );
  const { mutateAsync: classifyCase } = usePostCaseClassifications();
  const accountId =
    navAccountId || projectDetails?.account?.id || projectId || "";
  // Live-engineer-chat escalation (see the queued/engineer_assigned/
  // engineer_message/engineer_disconnected cases in the WS onEvent switch
  // below, and handleEscalateToEngineer/sendViaHumanChat further down this
  // component).
  const postChatEscalation = usePostChatEscalation(projectId ?? "");
  const postChatMessage = usePostChatMessage(projectId ?? "");
  const [isEscalating, setIsEscalating] = useState(false);
  const [isHumanConnected, setIsHumanConnected] = useState(false);
  const [assignedEngineerName, setAssignedEngineerName] = useState<
    string | null
  >(null);
  // The case the active escalation created — needed on every human-chat send
  // (see sendViaHumanChat) but not itself rendered, so a ref rather than
  // state. Cleared on engineer_disconnected/converted_to_case.
  const escalationCaseIdRef = useRef<string | null>(null);
  const [conversationId, setConversationId] = useState<string | null>(() => {
    if (urlConversationId) return urlConversationId;
    if (conversationResponse?.conversationId) {
      return conversationResponse.conversationId;
    }
    // LOCAL DEVELOPMENT ONLY (see
    // CUSTOMER_PORTAL_LOCAL_DEV_CLIENT_CONVERSATION_ID_ENABLED in
    // portalConfig.ts / public/config.js). Fabricates a client-side
    // conversation id up front so this chat session already has a non-empty
    // conversationId before the very first WebSocket message (see
    // sendViaWebSocket below, which always sends conversationId ?? "") --
    // letting backend-v2 skip its own CreateConversation call, which 404s
    // under entity-service's DATA_SOURCE=postgres (entity-service only
    // registers POST /conversations in ServiceNow mode). This lazy
    // useState initializer runs exactly once per chat session/mount, so the
    // same id is reused for every message afterwards; it is never
    // regenerated on re-render. Left unset/false, this branch never runs
    // and conversationId stays null exactly as it does today, until a real
    // urlConversationId/conversationResponse/conversation_created/final
    // value arrives -- production behavior is unchanged.
    if (window.config?.CUSTOMER_PORTAL_LOCAL_DEV_CLIENT_CONVERSATION_ID_ENABLED) {
      return crypto.randomUUID();
    }
    return null;
  });
  // Resolver for a pending "wait for the conversation id" promise (see
  // waitForConversationId). Resolved when conversation_created arrives, so a
  // case created moments after the first message still carries the id.
  const pendingIdResolveRef = useRef<((id: string | null) => void) | null>(null);

  const {
    data: conversationHistory,
    isLoading: isLoadingHistory,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = useGetConversationMessages(urlConversationId || "", { pageSize: 10 });
  const [isCreateCaseLoading, setIsCreateCaseLoading] = useState(false);
  const [isWaitingForClassification, setIsWaitingForClassification] =
    useState(false);
  const [messages, setMessages] = useState<Message[]>(() => {
    if (preloadedMessages && preloadedMessages.length > 0) {
      return preloadedMessages.map((message, index) => ({
        ...message,
        id: message.id || `restored-${index}`,
        sender:
          message.sender === ChatSender.BOT ? ChatSender.BOT : ChatSender.USER,
          isCurrentUser:
            message.sender === ChatSender.USER
              ? (message.isCurrentUser ?? true)
              : false,
        timestamp:
          message.timestamp instanceof Date
            ? message.timestamp
            : new Date(message.timestamp ?? Date.now()),
      }));
    }
    if (conversationResponse?.message) {
      const userMsg = initialUserMessage?.trim();
      const msgs: Message[] = [
        {
          id: "3",
          text: conversationResponse.message,
          sender: ChatSender.BOT,
          timestamp: new Date(),
          showCreateCaseAction: conversationResponse.actions != null,
          slotState: conversationResponse.slotState,
          recommendations:
            conversationResponse.recommendations?.recommendations,
        },
      ];
      if (userMsg) {
        msgs.unshift({
          id: "2",
          text: userMsg,
          sender: ChatSender.USER,
          isCurrentUser: true,
          timestamp: new Date(),
        });
      }
      return msgs;
    }
    if (urlConversationId) {
      return [];
    }

    const botWelcome: Message = {
      id: NOVERA_WELCOME_MESSAGE_ID,
      text: NOVERA_INITIAL_WELCOME_TEXT,
      sender: ChatSender.BOT,
      timestamp: new Date(),
    };
    return [botWelcome];
  });

  // Load and convert conversation history when resuming.
  useEffect(() => {
    if (!urlConversationId || !conversationHistory?.pages) return;
    if (lastProcessedConversationIdRef.current !== urlConversationId) {
      lastProcessedConversationIdRef.current = urlConversationId;
      processedHistoryPageCountRef.current = 0;
    }
    const pageCount = conversationHistory.pages.length;
    if (pageCount <= processedHistoryPageCountRef.current) return;
    processedHistoryPageCountRef.current = pageCount;

    const allMessages = conversationHistory.pages.flatMap(
      (page) => page.comments,
    );

    const convertedMessages: Message[] = allMessages
      .slice()
      .sort(compareByCreatedOnThenId)
      .map((msg, index) => {
        const isBot =
          msg.type?.toLowerCase() === "bot" ||
          msg.createdBy?.toLowerCase() === "novera";
        const messageCreatorEmail = msg.createdBy?.toLowerCase() ?? "";
        const isCurrentUserMessage =
          !isBot &&
          (currentUserEmail.length > 0
            ? messageCreatorEmail.length > 0 &&
              messageCreatorEmail === currentUserEmail
            : true);
        const createdByDisplayName = [
          msg.createdByFirstName,
          msg.createdByLastName,
        ]
          .filter((name) => Boolean(name && name.trim()))
          .join(" ")
          .trim();

        return {
          id: msg.id || `msg-${index}`,
          text: displayTextFromConversationContent(msg.content || "", isBot),
          sender: isBot ? ChatSender.BOT : ChatSender.USER,
          isCurrentUser: isBot ? false : isCurrentUserMessage,
          timestamp: dateFromApiCreatedOn(msg.createdOn),
          createdBy: createdByDisplayName || msg.createdBy || undefined,
          showCreateCaseAction: false,
        };
      });

    setMessages((prev) => {
      // Keep optimistic/nav-state messages when history API returns empty.
      if (convertedMessages.length === 0 && prev.length > 0) {
        return prev;
      }
      return convertedMessages;
    });
    queryClient.invalidateQueries({
      queryKey: [ApiQueryKeys.CONVERSATION_MESSAGES, urlConversationId, 10],
    });
  }, [urlConversationId, conversationHistory, queryClient, currentUserEmail]);


  // Update URL with conversationId from describe-issue flow
  useEffect(() => {
    if (
      !urlConversationId &&
      conversationResponse?.conversationId &&
      projectId
    ) {
      navigate(
        `/projects/${projectId}/support/chat/${conversationResponse.conversationId}`,
        { replace: true },
      );
    }
  }, [urlConversationId, conversationResponse, projectId, navigate]);

  // Wait (bounded) for the conversation id, which arrives asynchronously over
  // the chat WebSocket. Resolves immediately if it's already known; otherwise
  // when conversation_created fires, or with null after the timeout so case
  // creation is never blocked.
  const waitForConversationId = useCallback((): Promise<string | null> => {
    if (conversationId) {
      return Promise.resolve(conversationId);
    }
    return new Promise<string | null>((resolve) => {
      pendingIdResolveRef.current = resolve;
      setTimeout(() => {
        if (pendingIdResolveRef.current === resolve) {
          pendingIdResolveRef.current = null;
          resolve(null);
        }
      }, CONVERSATION_ID_WAIT_MS);
    });
  }, [conversationId]);

  const performClassification = useCallback(async () => {
    if (!projectId) {
      navigate("/");
      setIsCreateCaseLoading(false);
      setIsWaitingForClassification(false);
      return;
    }

    // Capture the conversation id (bounded wait) so the created case links back
    // to this chat; the backend then converts the chat on case creation. If the
    // id never arrives, we still proceed — case creation must never block.
    const chatConversationId = await waitForConversationId();

    try {
      const chatHistory = formatChatHistoryForClassification(messages);
      if (chatHistory) {
        try {
          const classificationResponse = await classifyCase({
            chatHistory,
            envProducts,
            region: DEFAULT_CONVERSATION_REGION,
            tier: DEFAULT_CONVERSATION_TIER,
            projectTypeId,
          });
          navigate(`/projects/${projectId}/support/chat/create-case`, {
            state: {
              messages,
              classificationResponse,
              conversationId: chatConversationId,
            },
          });
        } catch {
          navigate(`/projects/${projectId}/support/chat/create-case`, {
            state: { messages, conversationId: chatConversationId },
          });
        }
      } else {
        navigate(`/projects/${projectId}/support/chat/create-case`, {
          state: { messages, conversationId: chatConversationId },
        });
      }
    } finally {
      setIsCreateCaseLoading(false);
      setIsWaitingForClassification(false);
    }
  }, [
    projectId,
    navigate,
    messages,
    envProducts,
    classifyCase,
    projectTypeId,
    waitForConversationId,
  ]);

  const handleCreateCase = useCallback(() => {
    setIsCreateCaseLoading(true);

    // Always proceed — case creation must never block on the conversation id or
    // the WebSocket. performClassification waits (bounded) for the id so the
    // case links to the chat, then navigates.
    if (isAllProductsLoading) {
      setIsWaitingForClassification(true);
    } else {
      performClassification();
    }
  }, [isAllProductsLoading, performClassification]);

  useEffect(() => {
    if (isWaitingForClassification && !isAllProductsLoading) {
      setIsWaitingForClassification(false);
      performClassification();
    }
  }, [isWaitingForClassification, isAllProductsLoading, performClassification]);
  const [showRichText, setShowRichText] = useState(false);
  const [isInputDisabled, setIsInputDisabled] = useState(false);
  const [inputValue, setInputValue] = useState("");
  const inputValueRef = useRef("");
  const piiGuard = usePiiGuard();
  const [resetTrigger, setResetTrigger] = useState(0);
  const [isSending, setIsSending] = useState(false);
  // Mirrors isSending for the socket's onClose handler. The hook installs
  // ws.onclose during connect(), so that closure captures the options object
  // from whichever render connected — reading isSending directly there would
  // see a stale value. A ref is always current.
  const isSendingRef = useRef(false);
  useEffect(() => {
    isSendingRef.current = isSending;
  }, [isSending]);
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const activeBotMessageIdRef = useRef<string | null>(null);
  const initialMessageSentRef = useRef(false);
  const processedHistoryPageCountRef = useRef(0);
  const lastProcessedConversationIdRef = useRef<string | null>(null);
  const tokenQueueRef = useRef<string[]>([]);
  const pendingFinalRef = useRef<{
    payload: Record<string, unknown>;
    finalMessage: string;
  } | null>(null);
  const TYPING_INTERVAL_MS = CHAT_TYPING_INTERVAL_MS;
  const TYPING_CHARS_PER_TICK = CHAT_TYPING_CHARS_PER_TICK;

  const upsertActiveBotMessage = useCallback(
    (updater: (message: Message) => Message, fallback?: () => Message) => {
      setMessages((prev) => {
        const activeId = activeBotMessageIdRef.current;
        if (!activeId) return prev;
        let found = false;
        const next = prev.map((msg) => {
          if (msg.id !== activeId) return msg;
          found = true;
          return updater(msg);
        });
        if (!found && fallback) {
          next.push(fallback());
        }
        return next;
      });
    },
    [],
  );

  const flushPendingFinalIfReady = useCallback(() => {
    if (tokenQueueRef.current.length > 0) return;
    const pending = pendingFinalRef.current;
    if (!pending) return;
    const activeId = activeBotMessageIdRef.current;
    if (!activeId) return;

    let appliedFinal = false;
    flushSync(() => {
      setMessages((prev) => {
        const idx = prev.findIndex((m) => m.id === activeId);
        if (idx === -1) {
          return prev;
        }
        pendingFinalRef.current = null;
        const { finalMessage, payload } = pending;
        const msg = prev[idx];
        const parsedActions = Array.isArray(payload.actions)
          ? (payload.actions as NoveraAction[])
          : undefined;
        const next = [...prev];
        const answerId =
          typeof payload.messageId === "string" ? payload.messageId : undefined;
        next[idx] = {
          ...msg,
          isLoading: false,
          isError: false,
          text: finalMessage || msg.text,
          showCreateCaseAction: payload.actions != null,
          showFeedbackActions: !!answerId,
          feedbackMessageId: answerId,
          slotState: payload.slotState as SlotState | undefined,
          thinkingSteps: [],
          thinkingLabel: null,
          isStreaming: false,
          actions: parsedActions,
        };
        appliedFinal = true;
        return next;
      });
    });
    if (appliedFinal) {
      setIsSending(false);
      const actions = Array.isArray(pending?.payload?.actions)
        ? (pending.payload.actions as NoveraAction[])
        : [];
      if (actions.some((a) => a.type === NoveraActionType.SolutionProposed)) {
        setShowRichText(true);
      }
      if (actions.some((a) => a.type === NoveraActionType.SolutionWorked)) {
        setIsInputDisabled(true);
      }
    }
  }, [setShowRichText]);

  const dequeueOneTypedToken = useCallback(() => {
    const token = tokenQueueRef.current.shift();
    if (token === undefined) return;
    upsertActiveBotMessage((msg) => {
      const isPlaceholder =
        msg.text === NOVERA_ANALYZING_PLACEHOLDER_TEXT || msg.text === "";
      return {
        ...msg,
        isLoading: false,
        text: isPlaceholder ? token : `${msg.text}${token}`,
        isStreaming: true,
      };
    });
  }, [upsertActiveBotMessage]);

  useEffect(() => {
    const id = window.setInterval(() => {
      if (tokenQueueRef.current.length > 0) {
        dequeueOneTypedToken();
      }
      flushPendingFinalIfReady();
    }, TYPING_INTERVAL_MS);
    return () => window.clearInterval(id);
  }, [dequeueOneTypedToken, flushPendingFinalIfReady, TYPING_INTERVAL_MS]);

  const { connect, sendUserMessage, isConnected } = useChatWebSocket({
    onEvent: (event) => {
      switch (event.type) {
        case "conversation_created": {
          const nextConversationId = String(event.conversationId ?? "");
          if (nextConversationId) {
            setConversationId(nextConversationId);
            // Unblock a pending "wait for id" (e.g. a fast Create Case click)
            // so the case links to this chat.
            if (pendingIdResolveRef.current) {
              pendingIdResolveRef.current(nextConversationId);
              pendingIdResolveRef.current = null;
            }
            if (!urlConversationId && projectId) {
              navigate(
                `/projects/${projectId}/support/chat/${nextConversationId}`,
                {
                  replace: true,
                },
              );
            }
          }
          break;
        }
        case "thinking_start": {
          upsertActiveBotMessage(
            (msg) => ({
              ...msg,
              isLoading: false,
              text: NOVERA_ANALYZING_PLACEHOLDER_TEXT,
              thinkingSteps: [],
              thinkingLabel: null,
              isStreaming: false,
            }),
            () => ({
              id: `bot-${Date.now()}`,
              sender: ChatSender.BOT,
              timestamp: new Date(),
              text: NOVERA_ANALYZING_PLACEHOLDER_TEXT,
              thinkingSteps: [],
              thinkingLabel: null,
              isStreaming: false,
            }),
          );
          break;
        }
        case "thinking_step": {
          const label = String(event.label ?? event.step ?? "Working...");
          upsertActiveBotMessage((msg) => ({
            ...msg,
            isLoading: false,
            thinkingSteps: [...(msg.thinkingSteps ?? []), label],
            thinkingLabel: label,
          }));
          break;
        }
        case "thinking_end":
          upsertActiveBotMessage((msg) => ({
            ...msg,
            isLoading: false,
            thinkingLabel: msg.thinkingLabel,
          }));
          break;
        case "token": {
          const token = String(event.content ?? "");
          const cleaned = sanitizeStreamToken(token);
          if (cleaned.length === 0) {
            break;
          }
          for (const part of splitTokenForTyping(
            cleaned,
            TYPING_CHARS_PER_TICK,
          )) {
            tokenQueueRef.current.push(part);
          }
          break;
        }
        case "final": {
          const payload = (event.payload ?? {}) as Record<string, unknown>;
          const finalMessage = getFinalMessageFromPayload(payload);
          const nextConversationId = String(payload.conversationId ?? "");
          if (nextConversationId) {
            setConversationId(nextConversationId);
            if (!urlConversationId && projectId) {
              navigate(
                `/projects/${projectId}/support/chat/${nextConversationId}`,
                {
                  replace: true,
                },
              );
            }
          }
          pendingFinalRef.current = { payload, finalMessage };
          flushPendingFinalIfReady();
          const activeConversationId = nextConversationId || urlConversationId;
          if (activeConversationId) {
            queryClient.invalidateQueries({
              queryKey: [ApiQueryKeys.CONVERSATION_MESSAGES, activeConversationId, 10],
            });
          }
          break;
        }
        case "feedback_ack": {
          const ackId = String(event.messageId ?? "");
          const ackRating =
            event.rating === 1 ? 1 : event.rating === -1 ? -1 : null;
          // The server echoes what it actually stored, after dropping any tag it
          // does not recognise — so adopt its list unconditionally. An absent
          // `tags` field means it stored none (an older backend ignores the
          // field entirely), so fall back to [] rather than keeping our
          // optimistic value: showing chips as selected when nothing was saved
          // tells the user something untrue.
          const ackTags = Array.isArray(event.tags)
            ? (event.tags as unknown[]).filter(
                (t): t is string => typeof t === "string",
              )
            : undefined;
          if (ackId) {
            setMessages((prev) =>
              prev.map((m) =>
                m.feedbackMessageId === ackId
                  ? {
                      ...m,
                      feedbackRating: ackRating,
                      feedbackTags: ackTags ?? [],
                    }
                  : m,
              ),
            );
          }
          break;
        }
        // Live-engineer-chat: these arrive as a push from csm-portal/
        // backend into backend-v2's /internal/chat-events, relayed onto this
        // same connection (see backend-v2's handler.ChatEventsHandler /
        // WebSocketHandler.PushEvent) — no second socket. See
        // handleEscalateToEngineer/sendViaHumanChat below for the outbound
        // half of this feature.
        // "queued" arrives when the routing service had no available
        // engineer at escalation time (see csm-portal/backend's
        // HandleEscalate) — a simple informational bot message, no state
        // change: isEscalating stays true until engineer_assigned (or the
        // escalation error path) actually resolves it.
        case "queued": {
          const text = String(
            event.message ?? "You're in the queue. An engineer will be with you shortly.",
          );
          setMessages((prev) => [
            ...prev,
            {
              id: `queued-${Date.now()}`,
              text,
              sender: ChatSender.BOT,
              timestamp: new Date(),
            },
          ]);
          break;
        }
        case "engineer_assigned": {
          const engineer = String(
            event.engineerEmail ?? "a support engineer",
          );
          setIsEscalating(false);
          setIsHumanConnected(true);
          setAssignedEngineerName(engineer);
          setMessages((prev) => [
            ...prev,
            {
              id: `engineer-assigned-${Date.now()}`,
              text: `You're now connected with ${engineer}. They can see this conversation and will reply here.`,
              sender: ChatSender.BOT,
              timestamp: new Date(),
              isHumanMessage: true,
              engineerName: engineer,
            },
          ]);
          break;
        }
        case "engineer_message": {
          const text = String(event.message ?? "");
          if (!text) break;
          const engineer = String(
            event.engineerEmail ?? assignedEngineerName ?? "a support engineer",
          );
          setMessages((prev) => [
            ...prev,
            {
              id: `engineer-msg-${Date.now()}`,
              text,
              sender: ChatSender.BOT,
              timestamp: new Date(),
              isHumanMessage: true,
              engineerName: engineer,
            },
          ]);
          break;
        }
        case "engineer_disconnected": {
          setIsHumanConnected(false);
          setAssignedEngineerName(null);
          escalationCaseIdRef.current = null;
          setMessages((prev) => [
            ...prev,
            {
              id: `engineer-disconnected-${Date.now()}`,
              text: "The live chat session has ended. You're back with Novera, our AI assistant.",
              sender: ChatSender.BOT,
              timestamp: new Date(),
            },
          ]);
          break;
        }
        // Sent when the assigned engineer converts this chat into a case.
        // Mirrors engineer_disconnected's state resets since the live
        // session ends the same way, just for a different reason.
        case "converted_to_case": {
          setIsHumanConnected(false);
          setAssignedEngineerName(null);
          escalationCaseIdRef.current = null;
          setMessages((prev) => [
            ...prev,
            {
              id: `converted-to-case-${Date.now()}`,
              text: "This chat has been converted into a support case. The live chat has now ended.",
              sender: ChatSender.BOT,
              timestamp: new Date(),
            },
          ]);
          break;
        }
        case "error":
          pendingFinalRef.current = null;
          tokenQueueRef.current = [];
          upsertActiveBotMessage((msg) => ({
            ...msg,
            isLoading: false,
            isError: true,
            text: String(event.message ?? "Something went wrong"),
            thinkingSteps: [],
            isStreaming: false,
          }));
          setIsSending(false);
          break;
        default:
          break;
      }
    },
    onError: () => {
      pendingFinalRef.current = null;
      tokenQueueRef.current = [];
      upsertActiveBotMessage((msg) => ({
        ...msg,
        isLoading: false,
        isError: true,
        text: "WebSocket connection error.",
        thinkingSteps: [],
        isStreaming: false,
      }));
      setIsSending(false);
    },
    // A close is not always an error — the socket can drop mid-exchange (gateway
    // timeout, upstream reset) without onerror ever firing. Nothing else clears
    // isSending, and both send paths are guarded by it, so leaving it set makes
    // the composer permanently dead: the hook reconnects, isConnected flips back
    // to true, and every later message is silently swallowed with the typing
    // indicator still spinning. Recover only if a send was actually in flight,
    // so an idle close does not mark a finished answer as failed.
    onClose: () => {
      if (!isSendingRef.current) return;
      pendingFinalRef.current = null;
      tokenQueueRef.current = [];
      upsertActiveBotMessage((msg) => ({
        ...msg,
        isLoading: false,
        isError: true,
        text: "Connection lost before the answer arrived. Please try again.",
        thinkingSteps: [],
        isStreaming: false,
      }));
      setIsSending(false);
    },
  });

  const setInputValueAndRef = useCallback((v: string) => {
    inputValueRef.current = v;
    setInputValue(v);
  }, []);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  const sendViaWebSocket = useCallback(
    async (text: string): Promise<boolean> => {
      if (!projectId || !accountId) return false;
      const hasEnvProducts = Object.keys(envProducts).length > 0;
      const botMessageId = `bot-${Date.now()}`;
      activeBotMessageIdRef.current = botMessageId;
      pendingFinalRef.current = null;
      tokenQueueRef.current = [];

      setMessages((prev) => [
        ...prev,
        {
          id: `user-${Date.now()}`,
          text,
          sender: ChatSender.USER,
          isCurrentUser: true,
          timestamp: new Date(),
        },
        {
          id: botMessageId,
          text: "",
          sender: ChatSender.BOT,
          timestamp: new Date(),
          isLoading: true,
        },
      ]);
      setIsSending(true);

      try {
        await connect(projectId);
        await sendUserMessage({
          type: "user_message",
          accountId,
          conversationId: conversationId ?? "",
          message: text,
          envProducts: hasEnvProducts ? envProducts : {},
        });
        return true;
      } catch {
        tokenQueueRef.current = [];
        setMessages((prev) =>
          prev.map((m) =>
            m.id === botMessageId
              ? {
                  ...m,
                  isLoading: false,
                  isError: true,
                  text: "Could not connect to chatbot stream.",
                }
              : m,
          ),
        );
        setIsSending(false);
        return false;
      }
    },
    [
      accountId,
      connect,
      conversationId,
      envProducts,
      projectId,
      sendUserMessage,
    ],
  );

  // Sends a customer message once a live engineer has accepted the session
  // (see the "engineer_assigned" case above). Deliberately separate from
  // sendViaWebSocket: a human-attended message goes over REST to
  // backend-v2, which relays it to csm-portal/backend to be persisted as a
  // case comment — not over the AI chat WebSocket, and not persisted as a
  // conversation comment the way an AI-chat message is. See
  // usePostChatMessage's own doc comment.
  const sendViaHumanChat = useCallback(
    async (text: string): Promise<void> => {
      const caseId = escalationCaseIdRef.current;
      if (!conversationId || !caseId) return;

      setMessages((prev) => [
        ...prev,
        {
          id: `user-${Date.now()}`,
          text,
          sender: ChatSender.USER,
          isCurrentUser: true,
          timestamp: new Date(),
        },
      ]);

      try {
        await postChatMessage.mutateAsync({ conversationId, caseId, message: text });
      } catch (err) {
        // 410 means the session already ended server-side (the engineer
        // clicked "End session", or converted the chat to a case) before
        // this message reached it -- see csm-portal/backend's
        // HandleCustomerMessage and chat-routing-service's
        // router.ErrConversationEnded. Normally the "engineer_disconnected"/
        // "converted_to_case" WS event resets this page for exactly that
        // reason, but that push is a single best-effort attempt with no
        // retry (see backend-v2's WebSocketHandler.PushEvent) -- if it
        // never arrived, this page would otherwise keep behaving as though
        // the live chat were still ongoing, silently "failing" every
        // message the customer sends with no visible explanation. Perform
        // the identical reset here instead, rather than showing a generic
        // "please try again" that would never succeed.
        if ((err as ChatMessageApiError)?.status === 410) {
          setIsHumanConnected(false);
          setAssignedEngineerName(null);
          escalationCaseIdRef.current = null;
          setMessages((prev) => [
            ...prev,
            {
              id: `human-session-ended-${Date.now()}`,
              text: "The live chat session has ended. You're back with Novera, our AI assistant.",
              sender: ChatSender.BOT,
              timestamp: new Date(),
            },
          ]);
          return;
        }
        setMessages((prev) => [
          ...prev,
          {
            id: `human-send-error-${Date.now()}`,
            text: "Your message could not be delivered to the engineer. Please try again.",
            sender: ChatSender.BOT,
            timestamp: new Date(),
            isError: true,
          },
        ]);
      }
    },
    [conversationId, postChatMessage],
  );

  // Keeps the WebSocket alive (and registered for delivery) for the whole
  // duration of a live-engineer chat -- see HUMAN_CHAT_WS_HEARTBEAT_INTERVAL_MS's
  // own doc comment for why this is necessary: once escalated, every
  // customer message goes over REST (sendViaHumanChat above), so nothing
  // else ever touches this socket, and backend-v2 closes (and unregisters)
  // a connection it hasn't read from in 5 minutes purely for looking idle.
  // Sending "ping" with conversationId both resets that read deadline and
  // re-registers the connection in backend-v2's delivery map on every tick
  // (see registerConn in websocket.go, which runs before the isPing check),
  // so this also self-heals a connection that already dropped for some
  // other reason (a backend restart, a network blip) instead of leaving the
  // customer silently unreachable until they think to refresh.
  useEffect(() => {
    if (!isHumanConnected || !projectId || !conversationId) return;
    const id = window.setInterval(() => {
      void connect(projectId)
        .then(() => sendUserMessage({ type: "ping", conversationId }))
        .catch(() => {
          // Best-effort: the next tick tries again. Surfacing this to the
          // customer would be noise for what is, in the common case, a
          // single transient blip self-healed a minute later.
        });
    }, HUMAN_CHAT_WS_HEARTBEAT_INTERVAL_MS);
    return () => window.clearInterval(id);
  }, [isHumanConnected, projectId, conversationId, connect, sendUserMessage]);

  // Escalates the current conversation to a live engineer — see
  // usePostChatEscalation's own doc comment for what this call does
  // server-side. The engineer actually joining arrives later as the
  // "engineer_assigned" WS event above; this call only starts that process.
  const handleEscalateToEngineer = useCallback(async (): Promise<void> => {
    if (!conversationId || isEscalating || isHumanConnected) return;
    setIsEscalating(true);

    // Snapshot the conversation exactly as it's already visible on this
    // page, before appending the "connecting you" placeholder below -- see
    // buildEscalationPriorMessages's own doc comment for exactly what's
    // excluded and why.
    const priorMessages: EscalationPriorMessage[] =
      buildEscalationPriorMessages(messages);

    setMessages((prev) => [
      ...prev,
      {
        id: `escalation-pending-${Date.now()}`,
        text: "Connecting you to a live engineer — we've notified our support team and you'll be joined here as soon as one is available.",
        sender: ChatSender.BOT,
        timestamp: new Date(),
      },
    ]);
    try {
      const result = await postChatEscalation.mutateAsync({
        conversationId,
        customerName: currentUserEmail || undefined,
        priorMessages,
      });
      escalationCaseIdRef.current = result.caseId;
    } catch {
      setIsEscalating(false);
      setMessages((prev) => [
        ...prev,
        {
          id: `escalation-error-${Date.now()}`,
          text: 'We couldn\'t reach a live engineer right now. Please try again in a moment, or use "Create Case" instead.',
          sender: ChatSender.BOT,
          timestamp: new Date(),
          isError: true,
        },
      ]);
    }
  }, [
    conversationId,
    isEscalating,
    isHumanConnected,
    postChatEscalation,
    currentUserEmail,
    messages,
  ]);

  const handleSolutionWorked = useCallback(() => {
    if (isSending) return;
    void sendViaWebSocket("This Resolved My Issue");
  }, [isSending, sendViaWebSocket]);

  // Answer feedback (👍/👎) over the existing chat socket (#2534). Optimistic:
  // reflect the vote immediately, revert if the send fails. feedback_ack later
  // confirms the persisted value.
  // Latest feedback submission per messageId, so a stale rejection cannot
  // roll back a newer vote. A ref, not state: it must not trigger a render.
  const feedbackSeqRef = useRef<Map<string, number>>(new Map());

  const submitFeedback = useCallback(
    (messageId: string, rating: 1 | -1, tags?: string[]) => {
      if (!projectId) return;

      // Mark this as the latest submission for the message. Clicking 👍 then
      // 👎 leaves two sends in flight; if the first rejects after the second
      // resolved, its rollback must not undo the newer vote.
      const seq = (feedbackSeqRef.current.get(messageId) ?? 0) + 1;
      feedbackSeqRef.current.set(messageId, seq);

      // Remember what was showing so a failure restores it, rather than
      // clearing a rating the user had already given (and we had persisted).
      let previousRating: 1 | -1 | null = null;
      setMessages((prev) =>
        prev.map((m) => {
          if (m.feedbackMessageId !== messageId) return m;
          previousRating = m.feedbackRating ?? null;
          return {
            ...m,
            feedbackRating: rating,
            ...(tags ? { feedbackTags: tags } : {}),
          };
        }),
      );

      void connect(projectId)
        .then(() =>
          sendUserMessage({
            type: "feedback",
            messageId,
            rating,
            conversationId: conversationId ?? undefined,
            accountId: accountId || undefined,
            projectId: projectId || undefined,
            // Names, not just ids: a reviewer reading the dashboard should see
            // who this came from without resolving sys_ids by hand. Already
            // loaded for this page, so this costs no extra request.
            accountName: projectDetails?.account?.name || undefined,
            projectName: projectDetails?.name || undefined,
            conversationName: chatNumber || undefined,
            ...(tags ? { tags } : {}),
          }),
        )
        .catch(() => {
          if (feedbackSeqRef.current.get(messageId) !== seq) return;
          setMessages((prev) =>
            prev.map((m) =>
              m.feedbackMessageId === messageId
                ? { ...m, feedbackRating: previousRating }
                : m,
            ),
          );
        });
    },
    [
      accountId,
      connect,
      conversationId,
      projectId,
      projectDetails?.account?.name,
      projectDetails?.name,
      chatNumber,
      sendUserMessage,
    ],
  );

  /**
   * Toggle a reason tag on an answer that has already been rated. Re-sends the
   * same feedback event with the new list — the backend upserts on
   * (conversation, message), so this replaces rather than accumulating rows.
   * Capped client-side at MAX_FEEDBACK_TAGS; the server enforces the same limit.
   */
  const handleFeedbackTag = useCallback(
    (messageId: string, tag: string) => {
      const msg = messages.find((m) => m.feedbackMessageId === messageId);
      if (!msg?.feedbackRating) return;
      const current = msg.feedbackTags ?? [];
      const next = current.includes(tag)
        ? current.filter((t) => t !== tag)
        : current.length >= MAX_FEEDBACK_TAGS
          ? current
          : [...current, tag];
      if (next === current) return;
      submitFeedback(messageId, msg.feedbackRating, next);
    },
    [messages, submitFeedback],
  );

  const handleThumbsUp = useCallback(
    (messageId: string) => submitFeedback(messageId, 1),
    [submitFeedback],
  );
  const handleThumbsDown = useCallback(
    (messageId: string) => submitFeedback(messageId, -1),
    [submitFeedback],
  );

  // Feature-flagged (config.js): request a token limit increase over the chat
  // WebSocket. Kept off until the backend handler is ready.
  const tokenRequestEnabled =
    window.config?.CUSTOMER_PORTAL_NOVERA_TOKEN_REQUEST_ENABLED ?? false;

  // Feature-flagged (config.js): 👍/👎 on Novera answers. Passing the handlers
  // as undefined is what hides the row — ChatMessageBubble only renders it when
  // it has somewhere to send a rating — so no separate prop is needed.
  const feedbackEnabled =
    window.config?.CUSTOMER_PORTAL_NOVERA_FEEDBACK_ENABLED ?? false;

  // Feature-flagged (config.js): "Chat with an Engineer" escalation from
  // underneath a Novera reply. See handleEscalateToEngineer/sendViaHumanChat
  // and the engineer_assigned/engineer_message/engineer_disconnected cases
  // in the WS onEvent switch above for the rest of this feature.
  const liveEscalationEnabled =
    window.config?.CUSTOMER_PORTAL_NOVERA_LIVE_ESCALATION_ENABLED ?? false;
  // How many messages the customer has sent Novera so far — gates the
  // escalation CTA below (MIN_CUSTOMER_MESSAGES_BEFORE_ESCALATION) so it only
  // appears once they've given the AI assistant a real chance to help.
  // Derived from `messages` rather than tracked separately so restored/
  // preloaded history counts too, not just messages sent this session.
  const userMessageCount = useMemo(
    () => messages.filter((msg) => msg.sender === ChatSender.USER).length,
    [messages],
  );
  const [isTokenModalOpen, setIsTokenModalOpen] = useState(false);

  const handleTokenIncreaseSubmit = useCallback(
    async (reason: string): Promise<void> => {
      if (!projectId || !accountId) {
        throw new Error("Unable to submit the request right now.");
      }
      await connect(projectId);
      await sendUserMessage({
        type: "token_increase_request",
        accountId,
        accountName: projectDetails?.account?.name || undefined,
        requestedBy: currentUserEmail || undefined,
        reason,
        limitType: "session",
      });
    },
    [
      projectId,
      accountId,
      projectDetails?.account?.name,
      currentUserEmail,
      connect,
      sendUserMessage,
    ],
  );

  const handleSendMessage = useCallback(async (): Promise<boolean> => {
    const text = htmlToPlainText(inputValueRef.current).trim();
    if (!text || isSending || !projectId) return false;

    // Warn about PII before sending. The input is only cleared once the send
    // actually proceeds, so choosing "Edit" preserves the user's text.
    piiGuard.checkBeforeSubmit(text, () => {
      setInputValueAndRef("");
      setResetTrigger((prev) => prev + 1);
      // Once a live engineer has accepted the session, messages route to
      // them instead of the AI agent — see sendViaHumanChat's own doc
      // comment for why this is a separate path rather than a branch
      // inside sendViaWebSocket.
      if (isHumanConnected) {
        void sendViaHumanChat(text);
      } else {
        void sendViaWebSocket(text);
      }
    });
    return true;
  }, [
    isSending,
    projectId,
    isHumanConnected,
    sendViaHumanChat,
    sendViaWebSocket,
    setInputValueAndRef,
    piiGuard,
  ]);

  useEffect(() => {
    if (!initialUserMessage?.trim()) return;
    if (urlConversationId) return;
    if (initialMessageSentRef.current) return;
    initialMessageSentRef.current = true;
    void sendViaWebSocket(initialUserMessage.trim());
  }, [initialUserMessage, sendViaWebSocket, urlConversationId]);

  return (
    <Box
      sx={{
        height: (theme) => `calc(100vh - ${theme.spacing(21)})`,
        display: "flex",
      }}
    >
      <Box
        sx={{
          flex: 1,
          display: "flex",
          flexDirection: "column",
          width: "100%",
          overflow: "visible",
        }}
      >
        <Box sx={{ mt: -1.5, mx: -3 }}>
          <ChatHeader
            onBack={handleBack}
            onCreateCase={handleCreateCase}
            isCreateCaseLoading={isCreateCaseLoading || isAllProductsLoading}
            chatNumber={chatNumber}
          />
        </Box>

        {/* Chat window */}
        <Paper
          sx={{
            flex: 1,
            display: "flex",
            flexDirection: "column",
            overflow: "hidden",
          }}
        >
          {isLoadingHistory && urlConversationId && messages.length === 0 ? (
            <ChatSkeleton />
          ) : (
            <ChatMessageList
              messages={messages}
              messagesEndRef={messagesEndRef}
              onCreateCase={handleCreateCase}
              onThumbsUp={feedbackEnabled && isConnected ? handleThumbsUp : undefined}
              onThumbsDown={feedbackEnabled && isConnected ? handleThumbsDown : undefined}
            onFeedbackTag={isConnected ? handleFeedbackTag : undefined}
              onSolutionWorked={handleSolutionWorked}
              onRequestTokenIncrease={
                tokenRequestEnabled
                  ? () => setIsTokenModalOpen(true)
                  : undefined
              }
              onRequestEngineerEscalation={
                liveEscalationEnabled &&
                isConnected &&
                !!conversationId &&
                userMessageCount >= MIN_CUSTOMER_MESSAGES_BEFORE_ESCALATION &&
                !isEscalating &&
                !isHumanConnected
                  ? () => {
                      void handleEscalateToEngineer();
                    }
                  : undefined
              }
              onFetchOlder={
                urlConversationId && hasNextPage && !isFetchingNextPage
                  ? () => fetchNextPage()
                  : undefined
              }
              isFetchingOlder={isFetchingNextPage}
            />
          )}

          <Divider />

          <ChatInput
            onSend={handleSendMessage}
            inputValue={inputValue}
            setInputValue={setInputValueAndRef}
            onCreateCase={handleCreateCase}
            isCreateCaseLoading={isCreateCaseLoading || isAllProductsLoading}
            isSending={isSending}
            resetTrigger={resetTrigger}
            forceRichText={showRichText}
            disabled={isInputDisabled}
          />
        </Paper>
      </Box>

      <PiiWarningDialog {...piiGuard.dialogProps} />

      {tokenRequestEnabled && (
        <TokenRequestModal
          open={isTokenModalOpen}
          onClose={() => setIsTokenModalOpen(false)}
          onSubmit={handleTokenIncreaseSubmit}
        />
      )}
    </Box>
  );
}
