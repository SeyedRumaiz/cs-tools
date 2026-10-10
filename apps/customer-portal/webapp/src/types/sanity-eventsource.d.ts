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

// @sanity/eventsource ships types only for its Node entry point (its
// package.json declares "types": "./node.d.ts") — not for the browser
// bundle Vite actually resolves. This declares the minimal surface
// useCaseActivityStream.ts needs: an EventSource-compatible constructor that
// additionally accepts a `headers` option, which native browser EventSource
// does not support (see useCaseActivityStream.ts for why that's required).
declare module "@sanity/eventsource" {
  export default class EventSourcePolyfill extends EventTarget {
    constructor(url: string, eventSourceInitDict?: { headers?: Record<string, string> });
    readonly readyState: number;
    close(): void;
  }
}
