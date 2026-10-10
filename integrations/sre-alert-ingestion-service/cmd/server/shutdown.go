// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
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

package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// drainer is the part of the server shutdown needs; *server.Server implements it.
type drainer interface {
	StartDraining()
}

// closer is the allocator's shutdown; *allocator.Allocator implements it.
type closer interface {
	Close(ctx context.Context) error
}

// payloadCloser writes buffered raw bodies before exit; *payloads.Buffer implements it.
type payloadCloser interface {
	Close(ctx context.Context)
}

// budget splits server.shutdown_grace between the shutdown steps.
type budget struct {
	DrainDelay     time.Duration
	RequestWait    time.Duration
	AllocatorDrain time.Duration
	PayloadDrain   time.Duration
}

// shutdown drains /healthz, in-flight requests, the allocator, then buffered raw bodies, each in its own budget within server.shutdown_grace; after runs last with what is left.
func shutdown(ctx context.Context, logger *slog.Logger, srv drainer, httpSrv *http.Server, alloc closer, raw payloadCloser, b budget, after ...func(context.Context)) {
	start := time.Now()
	srv.StartDraining()
	select {
	case <-time.After(b.DrainDelay):
	case <-ctx.Done():
	}

	httpCtx, cancelHTTP := context.WithDeadline(ctx, start.Add(b.DrainDelay+b.RequestWait))
	if err := httpSrv.Shutdown(httpCtx); err != nil {
		logger.Error("http shutdown incomplete", "error", err)
	}
	cancelHTTP()

	allocCtx, cancelAlloc := context.WithTimeout(context.Background(), b.AllocatorDrain)
	if err := alloc.Close(allocCtx); err != nil {
		logger.Error("allocator did not drain within allocator_drain; unwritten batches were answered 503", "error", err)
	}
	cancelAlloc()

	if raw != nil {
		// A fresh window like the allocator's, so a slow request or allocator drain can't leave the final insert an expired context.
		rawCtx, cancelRaw := context.WithTimeout(context.Background(), b.PayloadDrain)
		raw.Close(rawCtx)
		cancelRaw()
	}

	for _, f := range after {
		f(ctx)
	}
	logger.Info("shutdown complete")
}
