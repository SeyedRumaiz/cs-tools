# Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
#
# WSO2 LLC. licenses this file to you under the Apache License,
# Version 2.0 (the "License"); you may not use this file except
# in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.

FROM golang:1.26.0-alpine AS builder

WORKDIR /app

COPY . .

# Run unit tests (the integration suite is behind a build tag and needs a local Cassandra).
RUN go test ./...

RUN go build -o sre-alert-ingestion-service ./cmd/server

# Pinned for reproducible builds.
FROM alpine:3.22

WORKDIR /app

COPY --from=builder /app/sre-alert-ingestion-service .

# Choreo requires a non-root user with a UID/GID in 10000-20000.
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/nonexistent" \
    --shell "/sbin/nologin" \
    --no-create-home \
    --uid 10014 \
    "choreo"

USER 10014

ENTRYPOINT ["./sre-alert-ingestion-service"]
