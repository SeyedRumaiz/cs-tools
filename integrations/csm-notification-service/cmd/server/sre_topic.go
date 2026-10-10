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

package main

import (
	"fmt"
	"strings"
)

// consumerTarget is one topic and the consumer group that reads it.
type consumerTarget struct {
	Topic string
	Group string
}

const (
	defaultSREConsumerGroup    = "csm-notification-service-sre"
	defaultSREDLQConsumerGroup = "csm-notification-service-sre-dlq"
)

// srePlan says which consumers to run once SRE_EVENT_HUB_TOPIC is set.
type srePlan struct {
	// Enabled is false when no SRE topic is configured: every existing
	// consumer runs exactly as before and nothing below applies.
	Enabled bool
	SRE     consumerTarget
	SREDLQ  consumerTarget
	// StartCR / StartOutage (and their DLQs) are false when that consumer's
	// topic IS the SRE topic -- the SRE consumer reads it instead, and two
	// consumers in different groups on one topic would send every email twice.
	StartCR, StartCRDLQ, StartOutage, StartOutageDLQ bool
}

// planSREConsumers decides the SRE consumer from configuration.
//
// *** WHICH GROUP, AND WHY IT MATTERS. *** Consumers here start a NEW group at
// the beginning of the topic (kafka.FirstOffset). A new group on a topic that
// another of our groups has already read would re-send every email still
// retained in it. So when the SRE topic is one the CR or outage consumer
// already reads, the SRE consumer takes over THAT group and carries on from
// its committed offset. Only a topic nobody here reads yet gets the new
// default group, where starting from the beginning is what is wanted. An
// explicit SRE_CONSUMER_GROUP / SRE_DLQ_CONSUMER_GROUP always wins.
func planSREConsumers(sreTopic, sreGroup, sreDLQTopic, sreDLQGroup string,
	cr, crDLQ, outage, outageDLQ consumerTarget) srePlan {
	// Trimmed as entity-service trims SRE_EVENT_HUB_TOPIC: a stray space in
	// the shared value must not leave the two services on different topics,
	// entity-service publishing to "sre-events" while this reads "sre-events ".
	sreTopic, sreGroup = strings.TrimSpace(sreTopic), strings.TrimSpace(sreGroup)
	sreDLQTopic, sreDLQGroup = strings.TrimSpace(sreDLQTopic), strings.TrimSpace(sreDLQGroup)
	if sreTopic == "" {
		return srePlan{StartCR: true, StartCRDLQ: true, StartOutage: true, StartOutageDLQ: true}
	}
	if sreDLQTopic == "" {
		sreDLQTopic = sreTopic + "-dlq"
	}
	pick := func(topic, explicit, fallback string, candidates ...consumerTarget) string {
		if explicit != "" {
			return explicit
		}
		for _, c := range candidates {
			if c.Topic == topic && c.Group != "" {
				return c.Group
			}
		}
		return fallback
	}
	return srePlan{
		Enabled:        true,
		SRE:            consumerTarget{sreTopic, pick(sreTopic, sreGroup, defaultSREConsumerGroup, outage, cr)},
		SREDLQ:         consumerTarget{sreDLQTopic, pick(sreDLQTopic, sreDLQGroup, defaultSREDLQConsumerGroup, outageDLQ, crDLQ)},
		StartCR:        cr.Topic != sreTopic,
		StartCRDLQ:     crDLQ.Topic != sreDLQTopic,
		StartOutage:    outage.Topic != sreTopic,
		StartOutageDLQ: outageDLQ.Topic != sreDLQTopic,
	}
}

// validateSREPlan rejects an enabled plan whose topics would make a consumer
// read its own output or another consumer's input. Checked before any
// consumer starts.
//
//   - The SRE topic or its DLQ is the case or project topic: those topics
//     have consumers with their own semantics, and reading them a second time
//     under another group sends their emails twice.
//   - The DLQ is the SRE topic itself: a record that keeps failing is
//     dead-lettered by republishing it unchanged, so it would land back on
//     the topic it failed on, be read again, fail again, and be republished
//     again -- an endless stream of copies ahead of the valid events.
func validateSREPlan(plan srePlan, caseTopic, projectTopic string) error {
	if !plan.Enabled {
		return nil
	}
	for _, t := range []struct{ name, topic string }{
		{"SRE_EVENT_HUB_TOPIC", plan.SRE.Topic},
		{"SRE_EVENT_HUB_DLQ_TOPIC", plan.SREDLQ.Topic},
	} {
		if t.topic == caseTopic || t.topic == projectTopic {
			return fmt.Errorf("%s must not be the case or project topic (%q)", t.name, t.topic)
		}
	}
	if plan.SREDLQ.Topic == plan.SRE.Topic {
		return fmt.Errorf("SRE_EVENT_HUB_DLQ_TOPIC must differ from SRE_EVENT_HUB_TOPIC (both %q)", plan.SRE.Topic)
	}
	return nil
}
