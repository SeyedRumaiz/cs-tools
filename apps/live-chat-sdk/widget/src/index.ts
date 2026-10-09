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

import { defineLiveChatWidget } from "./element.js";

export {
  DEFAULT_ASSISTANT,
  DEFAULT_LAYOUT,
  DEFAULT_TEXTS,
  DEFAULT_THEME,
  LiveChatConfigError,
  resolveConfig,
  type LiveChatAssistant,
  type LiveChatLayout,
  type LiveChatPosition,
  type LiveChatTexts,
  type LiveChatTheme,
  type LiveChatWidgetConfig,
  type ResolvedLiveChatConfig,
} from "./config.js";
export {
  defineLiveChatWidget,
  LIVE_CHAT_EVENTS,
  LiveChatWidgetElement,
  type AccessTokenProvider,
  type LiveChatRequestTransport,
  type LiveChatStreamTransport,
  type LiveChatTransport,
  type LiveChatTransportResponse,
  type LiveChatEndReason,
  type LiveChatEndedDetail,
  type LiveChatOpenOptions,
  type LiveChatPhase,
  type LiveChatPriorMessage,
  type LiveChatTranscriptEntry,
} from "./element.js";

// Importing the package registers <wso2-live-chat>.
defineLiveChatWidget();
