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

package paging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/chataudience"
	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/eventbus"
	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/events"
	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/notifications"
	"os"
)

// callPlacer abstracts notifications.TwilioClient's two call methods.
type callPlacer interface {
	MakeSSMLCall(ctx context.Context, to string, speech notifications.Speech) (notifications.Call, error)
	MakeCall(ctx context.Context, to, message string) (notifications.Call, error)
}

// ladderStore abstracts Store for testability.
type ladderStore interface {
	Create(ctx context.Context, incidentID string, st LadderState) (bool, error)
	Save(ctx context.Context, incidentID string, st LadderState) error
	Get(ctx context.Context, incidentID string) (LadderState, bool, error)
	Delete(ctx context.Context, incidentID string) error
	AddWake(ctx context.Context, member string, at time.Time) error
	RemoveWakes(ctx context.Context, members ...string) error
	DueMembers(ctx context.Context, now time.Time) ([]string, error)
	// MarkCalled records who was reached, for the evening pairing's
	// round-robin. Best-effort at every call site.
	MarkCalled(ctx context.Context, email string, at time.Time) error
	// CaseAssignee and SetCaseAssignee hold a customer case's current
	// assignee, which the case events that start a chain do not carry.
	CaseAssignee(ctx context.Context, caseID string) (string, error)
	SetCaseAssignee(ctx context.Context, caseID, email string) error
	// ClaimCall takes one due call for this replica for ttl, so two replicas
	// ticking together cannot both dial it; ReleaseCall gives it back.
	ClaimCall(ctx context.Context, member string, ttl time.Duration) (bool, error)
	ReleaseCall(ctx context.Context, member string) error
	// The Special Ops page's dedup (sme.go): one open page per incident and
	// SME team, closed by an assignment.
	OpenSMEPage(ctx context.Context, incidentID, team string, changedOn time.Time, ttl time.Duration) (bool, error)
	CloseSMEPage(ctx context.Context, incidentID, team string) error
	CloseSMEPages(ctx context.Context, incidentID string, answeredAt time.Time) ([]string, error)
	SMEClosedThrough(ctx context.Context, incidentID string) (time.Time, error)
}

// callClaimTTL bounds a claim on one due call. Long enough to cover placing
// the call; short enough that a replica dying mid-call delays it by at most
// this, and never loses it -- the wake entry is still there.
const callClaimTTL = time.Minute

// incidentNotes abstracts the entity-service client that writes the execution
// summary back onto the incident (section 11.0).
type incidentNotes interface {
	AppendWorkNote(ctx context.Context, incidentID, note string) error
	AppendCaseWorkNote(ctx context.Context, caseID, note string) error
}

// EngineConfig holds the engine's operational switches.
type EngineConfig struct {
	// CallSendingEnabled is the killswitch, mirroring
	// dispatch.Dispatcher.callSendingEnabled (CALL_SENDING_ENABLED). When
	// false the ladder still runs, still records, and still writes its work
	// note — it just logs each call instead of dialling, so a deployment can
	// watch a real ladder end to end without paging anyone.
	CallSendingEnabled bool
	// UseSSML picks Trigger.VoiceSpeech over Trigger.VoiceMessagePlain.
	UseSSML bool
	// Channel selects how a rung reaches people: the specification's phone
	// call, a card in the incident's Google Chat space, or both. Defaults to
	// calls — see ParseChannel for why silently downgrading a pager would be
	// the wrong default.
	Channel Channel
	// Ladder is this ladder's section of the configuration file: which
	// incidents get a ladder at all, and what one ladder may spend. The zero
	// value has no opinion on either, which is what a deployment with no
	// configuration file gets.
	Ladder LadderConfig
	// Routing decides which ladders an incident climbs; both engines read the
	// same rules and each asks only about its own ladder. The zero value is
	// DefaultRouting.
	Routing Routing
	// Kind is which ladder this engine runs. The service runs one engine per
	// ladder, each with its own channel, consumer group and store namespace,
	// because a P0 CRE incident climbs both at once. The zero value is the
	// CRE ladder, which is what every engine was before the SRE one existed.
	Kind Ladder
	// SME is the Special Ops page, placed by the SRE engine alone. The zero
	// value is off.
	SME SMEConfig
}

// Engine runs the incident call-escalation ladder.
//
// Two halves, the same split internal/slaengine uses: Handle is the consumer
// side (its own consumer group — see cmd/server/main.go), reacting to the three
// signals that start and stop a ladder; Tick is the scheduler side, placing
// whatever calls have come due. Neither knows the timing rules, which live
// entirely in policy.go and plan.go.
type Engine struct {
	policies map[string]PriorityPolicy
	resolver Resolver
	// notifiers is how a rung reaches people, one per selected channel. The
	// ladder's rules know nothing about which are in play.
	notifiers []notifier
	// missingChannels names every channel cfg.Channel selected that has no
	// client to serve it. Recorded rather than rejected: a deployment asking
	// for both channels with only one client configured should still page
	// people over the one it has, and refusing to construct would take the
	// working half down with the broken one. But it must not be silent --
	// a ladder that posts no chat card while an operator believes it does is
	// precisely the failure this exists to surface.
	missingChannels []Channel
	// smeNotifiers is how the Special Ops page reaches the SME, from the
	// file's sme section; nil on an engine that does not place it.
	smeNotifiers []notifier
	// smeTeamNotifiers is set only when sme.teamChats is: each listed SME
	// team's notifiers, posting to its own Chat space. A team not listed
	// gets smeNoChatNotifiers, the SME channel without chat.
	smeTeamNotifiers   map[string][]notifier
	smeNoChatNotifiers []notifier
	// teamNotifiers is set only when the SRE ladder's teamChats is: each
	// listed team's notifiers, posting its rung cards to its own space.
	teamNotifiers map[string][]notifier
	// teamRooms and sharedRoom are where the closing message of an
	// unanswered ladder goes (sre.unansweredChat): the team's own space, else
	// the ladder's chat space. Built whatever the channel is.
	teamRooms  map[string]chatRoom
	sharedRoom *chatRoom
	// smeRooms and smeSharedRoom are where an SME ladder's closing message
	// goes (sme.unansweredChat): the SME team's own space, else sme.chat
	// when sme.teamChats is unset.
	smeRooms      map[string]chatRoom
	smeSharedRoom *chatRoom
	links         PortalLinks
	store         ladderStore
	notes         incidentNotes
	cfg           EngineConfig
	// clock is time.Now unless a test substitutes one; the staleness check
	// in start is the only thing that reads it, and it has to be testable
	// against a trigger that is genuinely old.
	clock func() time.Time
}

func (e *Engine) now() time.Time {
	if e.clock != nil {
		return e.clock()
	}
	return time.Now()
}

// NewEngine constructs an Engine. notes may be nil, in which case the
// execution summary is logged rather than written back to the incident (see
// writeNote) — a deployment without entity-service access still runs a real
// ladder.
//
// The nil check is deliberate and must stay: assigning a nil *EntityClient
// straight into the incidentNotes interface field would store a non-nil
// interface holding a nil pointer, so writeNote's `e.notes == nil` would be
// false and it would call AppendWorkNote on a nil receiver.
func NewEngine(policies map[string]PriorityPolicy, resolver Resolver, calls *notifications.TwilioClient, chat *notifications.GoogleChatClient, links PortalLinks, store *Store, notes *EntityClient, defaultChatProduct string, cfg EngineConfig) *Engine {
	e := &Engine{policies: policies, resolver: resolver, cfg: cfg}
	if store != nil {
		// Not assigned when nil, for the same reason notes is not: a nil
		// *Store in the interface field would not compare equal to nil.
		e.store = store.ForLadder(cfg.Kind)
	}
	if cfg.Kind == LadderSRE {
		// The SRE clock is the file's sre.timing, not section 7.0's table.
		e.policies = withSREPolicy(policies, cfg.Ladder.Timing.Policy())
		// The SME ladder runs in the SRE engine on its own clock.
		e.policies = withPolicy(e.policies, SMEPolicyKey, cfg.SME.Policy())
	}
	if notes != nil {
		e.notes = notes
	}
	e.notifiers, e.missingChannels = buildNotifiers(cfg.Channel, cfg.Ladder.Chat, cfg.UseSSML, calls, chat, links, defaultChatProduct)
	// Logged here rather than per attempt: this is a deployment mistake, it
	// cannot change while the process runs, and one line at startup is
	// actionable where one line per rung is noise. place() still reports the
	// case where nothing at all is configured, because that one reaches
	// nobody.
	for _, ch := range e.missingChannels {
		slog.Error("escalation: configured for a channel with no client; nothing will be sent over it",
			"missingChannel", string(ch), "configuredChannel", string(cfg.Channel))
	}
	e.links = links
	if cfg.Kind == LadderSRE {
		e.buildTeamChats(cfg, calls, chat, links, defaultChatProduct)
	}
	if cfg.Kind == LadderSRE && cfg.SME.Enabled {
		var missing []Channel
		e.smeNotifiers, missing = buildNotifiers(cfg.SME.Channel, cfg.SME.Chat, cfg.UseSSML, calls, chat, links, defaultChatProduct)
		for _, ch := range missing {
			slog.Error("escalation: the SME page is configured for a channel with no client; nothing will be sent over it",
				"missingChannel", string(ch), "configuredChannel", string(cfg.SME.Channel))
		}
		if len(cfg.SME.TeamChats) > 0 {
			e.smeTeamNotifiers = map[string][]notifier{}
			for team, c := range cfg.SME.TeamChats {
				e.smeTeamNotifiers[teamKeyFor(team)], _ = buildNotifiers(cfg.SME.Channel, c, cfg.UseSSML, calls, chat, links, defaultChatProduct)
			}
			e.smeNoChatNotifiers = withoutChat(e.smeNotifiers)
		}
	}
	return e
}

// chatRoom is one Google Chat space a message can be posted to.
type chatRoom struct {
	chat chatSender
	room string
}

// buildTeamChats wires the SRE ladder's per-team spaces (teamChats) and the
// rooms its closing message goes to (unansweredChat).
func (e *Engine) buildTeamChats(cfg EngineConfig, calls *notifications.TwilioClient, chat *notifications.GoogleChatClient, links PortalLinks, defaultChatProduct string) {
	if len(cfg.Ladder.TeamChats) > 0 {
		e.teamNotifiers = map[string][]notifier{}
		for team, c := range cfg.Ladder.TeamChats {
			e.teamNotifiers[teamKeyFor(team)], _ = buildNotifiers(cfg.Channel, c, cfg.UseSSML, calls, chat, links, defaultChatProduct)
		}
	}
	if cfg.SME.Enabled && cfg.SME.UnansweredChat {
		e.smeRooms = map[string]chatRoom{}
		for team, c := range cfg.SME.TeamChats {
			if client, room := ladderChat(c, chat, defaultChatProduct, os.Getenv); client != nil {
				e.smeRooms[teamKeyFor(team)] = chatRoom{chat: client, room: room}
			}
		}
		if len(cfg.SME.TeamChats) == 0 {
			if client, room := ladderChat(cfg.SME.Chat, chat, defaultChatProduct, os.Getenv); client != nil {
				e.smeSharedRoom = &chatRoom{chat: client, room: room}
			}
		}
	}
	if !cfg.Ladder.UnansweredChat && !cfg.Ladder.SMEHandoffChat {
		return
	}
	e.teamRooms = map[string]chatRoom{}
	for team, c := range cfg.Ladder.TeamChats {
		if client, room := ladderChat(c, chat, defaultChatProduct, os.Getenv); client != nil {
			e.teamRooms[teamKeyFor(team)] = chatRoom{chat: client, room: room}
		}
	}
	if client, room := ladderChat(cfg.Ladder.Chat, chat, defaultChatProduct, os.Getenv); client != nil {
		e.sharedRoom = &chatRoom{chat: client, room: room}
	} else if len(e.teamRooms) == 0 {
		slog.Error("escalation: sre.unansweredChat or sre.smeHandoffChat is on but no Google Chat space is configured; nothing will be posted")
	}
}

// sreTeamKey is the SRE team an incident is assigned to, as a teamChats key:
// its assignment group through sre.teams.aliases, lower case.
func (e *Engine) sreTeamKey(team string) string {
	key := teamKeyFor(team)
	for group, alias := range e.cfg.Ladder.Teams.Aliases {
		if teamKeyFor(group) == key {
			return teamKeyFor(alias)
		}
	}
	return key
}

// notifiersFor is how one plan's rungs reach people: its team's own space
// when the SRE ladder's teamChats lists the team, else the ladder's own.
func (e *Engine) notifiersFor(plan Plan) []notifier {
	if e.teamNotifiers != nil {
		if n, ok := e.teamNotifiers[e.sreTeamKey(plan.Trigger.Team)]; ok {
			return n
		}
	}
	return e.notifiers
}

// postUnanswered tells the team's space that a ladder called every rung and
// nobody acknowledged the incident. Best effort: a failure is logged, never
// retried -- the ladder is over either way.
func (e *Engine) postUnanswered(ctx context.Context, st LadderState) {
	t := st.Plan.Trigger
	if e.cfg.Kind != LadderSRE {
		return
	}
	target, ok, chain := e.unansweredRoom(t)
	if !ok {
		return
	}
	if !target.chat.HasAudienceSpace(target.room) {
		slog.WarnContext(ctx, "escalation: no Google Chat space for the closing message", "incidentId", t.IncidentID, "audience", target.room)
		return
	}
	var called []string
	for i, done := range st.Placed {
		if done && i < len(st.Plan.Calls) && st.Plan.Calls[i].Recipient.Name != "" {
			c := st.Plan.Calls[i]
			label := c.Level.String()
			if tier, ok := sreTier[c.Level]; ok {
				label = tier
			}
			called = append(called, label+" "+c.Recipient.Name)
		}
	}
	portal, label := e.links.IncidentLink(t.IncidentID), "View incident"
	if t.isCase() {
		portal, label = e.links.CaseLink(t.IncidentID), "View case"
	}
	err := target.chat.SendEscalationUnanswered(ctx, notifications.EscalationUnanswered{
		Audience:    target.room,
		Chain:       chain,
		IncidentRef: t.caseRef(),
		Title:       t.Title,
		Priority:    t.Priority,
		Team:        t.Team,
		Called:      called,
		Instruction: t.instruction(false),
		PortalURL:   portal,
		PortalLabel: label,
		ThreadKey:   "incident-escalation-" + t.IncidentID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "escalation: the closing message was not posted", "incidentId", t.IncidentID, "audience", target.room, "err", err)
		return
	}
	slog.InfoContext(ctx, "escalation: closing message posted", "incidentId", t.IncidentID, "audience", target.room)
}

// unansweredRoom is where a finished, unanswered ladder's closing message
// goes, and the chain it names; ok is false when it goes nowhere.
func (e *Engine) unansweredRoom(t Trigger) (chatRoom, bool, string) {
	if t.Routing.Ladder == LadderSME {
		if !e.cfg.SME.UnansweredChat {
			return chatRoom{}, false, ""
		}
		chain := smeTeamLabel(t.Routing.SMETeam) + " SME"
		if r, ok := e.smeRooms[teamKeyFor(t.Routing.SMETeam)]; ok {
			return r, true, chain
		}
		if e.smeSharedRoom != nil {
			return *e.smeSharedRoom, true, chain
		}
		return chatRoom{}, false, ""
	}
	if !e.cfg.Ladder.UnansweredChat {
		return chatRoom{}, false, ""
	}
	if r, ok := e.teamRooms[e.sreTeamKey(t.Team)]; ok {
		return r, true, t.Routing.SREChain()
	}
	if e.sharedRoom != nil {
		return *e.sharedRoom, true, t.Routing.SREChain()
	}
	return chatRoom{}, false, ""
}

// sreRoomFor is the SRE space for a team: its own (sre.teamChats), else the
// shared one; ok is false when neither is configured.
func (e *Engine) sreRoomFor(team string) (chatRoom, bool) {
	if r, ok := e.teamRooms[e.sreTeamKey(team)]; ok {
		return r, true
	}
	if e.sharedRoom != nil {
		return *e.sharedRoom, true
	}
	return chatRoom{}, false
}

// smeTeamLabel is an SME team key as a reader would write it: "b-central" is
// "B-Central", "choreo-runtime" is "Choreo Runtime".
func smeTeamLabel(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if strings.EqualFold(key, "b-central") {
		return "B-Central"
	}
	words := strings.FieldsFunc(key, func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
	for i, w := range words {
		if len(w) <= 2 {
			words[i] = strings.ToUpper(w)
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// smeNotifiersFor is how the SME page reaches one SME team: its own Chat
// space when sme.teamChats lists it, no chat card when sme.teamChats is set
// but does not list it, and the shared SME space when sme.teamChats is unset.
func (e *Engine) smeNotifiersFor(team string) []notifier {
	if e.smeTeamNotifiers == nil {
		return e.smeNotifiers
	}
	if n, ok := e.smeTeamNotifiers[teamKeyFor(team)]; ok {
		return n
	}
	return e.smeNoChatNotifiers
}

// smeTeamHasNoChat reports whether a team's page posts no card only because
// sme.teamChats does not list it.
func (e *Engine) smeTeamHasNoChat(team string) bool {
	if e.smeTeamNotifiers == nil || !e.cfg.SME.Channel.Uses(ChannelChat) {
		return false
	}
	_, ok := e.smeTeamNotifiers[teamKeyFor(team)]
	return !ok
}

// withoutChat is notifiers with the chat ones left out.
func withoutChat(notifiers []notifier) []notifier {
	out := []notifier{}
	for _, n := range notifiers {
		if _, isChat := n.(chatNotifier); !isChat {
			out = append(out, n)
		}
	}
	return out
}

// buildNotifiers wires one notifier per channel selected, and names every
// selected channel that has no client to serve it.
func buildNotifiers(channel Channel, chatCfg Chat, useSSML bool, calls *notifications.TwilioClient, chat *notifications.GoogleChatClient, links PortalLinks, defaultChatProduct string) ([]notifier, []Channel) {
	var out []notifier
	var missing []Channel
	// ChannelLog needs no client and so can never be half-configured — there
	// is no missingChannels case for it. It is also exclusive: Uses() reports
	// false for both real channels here, so a ladder set to log wires this
	// notifier and nothing else, which is the point. A log ladder that also
	// dialled would be the worst of both.
	if channel == ChannelLog {
		out = append(out, logNotifier{})
	}
	if channel.Uses(ChannelCall) {
		if calls == nil {
			missing = append(missing, ChannelCall)
		} else {
			out = append(out, voiceNotifier{calls: calls, useSSML: useSSML})
		}
	}
	if channel.Uses(ChannelChat) {
		// The ladder's own webhook, when configured, replaces the shared
		// client -- and must be resolved before deciding chat is unavailable,
		// since a deployment may have a webhook for the ladder and no
		// GOOGLE_CHAT_SPACES at all.
		var room string
		chat, room = ladderChat(chatCfg, chat, defaultChatProduct, os.Getenv)
		if chat == nil {
			missing = append(missing, ChannelChat)
		} else {
			// links goes in only when it is really there, for the same reason
			// links is a value type on purpose. It used to be a
			// *recipientlinks.Resolver, and assigning a nil one straight into
			// the incidentLinker field stored a non-nil interface holding a
			// nil pointer, so the nil check inside Deliver passed and
			// IncidentLink was called on a nil receiver — a panic in the
			// middle of paging somebody, made twice in this one constructor
			// before a real run caught it. A PortalLinks with no base URL
			// simply returns an empty link, which the card renders as no link,
			// so there is no nil to get wrong any more.
			out = append(out, chatNotifier{chat: chat, audience: room, links: links})
		}
	}
	return out, missing
}

// Handle implements eventbus.Handle for this engine's own consumer group.
//
// It shares a topic with every other event in this service, so anything that
// is not one of its three signals is a silent no-op rather than an error —
// same reasoning as slaengine.Engine.Handle and dispatch.Handle's own no-op
// cases. Erroring would burn this consumer's retries and dead-letter a record
// that was never broken.
func (e *Engine) Handle(ctx context.Context, record eventbus.Record) error {
	var env events.Envelope
	if err := json.Unmarshal(record.Value, &env); err != nil {
		return fmt.Errorf("escalation: decode envelope: %w", err)
	}
	switch env.Type {
	case events.TypeIncidentCreated, events.TypeIncidentPriorityElevated,
		events.TypeIncidentAcknowledged, events.TypeIncidentCommentAdded,
		events.TypeIncidentAssigned,
		events.TypeCaseCreated, events.TypeSeverityChanged, events.TypeCaseAssigned,
		events.TypeCommentAdded, events.TypeStatusChanged:
	default:
		return nil
	}
	if err := events.Validate(env.EntityID, env.Type, env.Payload); err != nil {
		return fmt.Errorf("escalation: invalid payload: %w", err)
	}

	switch env.Type {
	case events.TypeCaseCreated, events.TypeSeverityChanged, events.TypeCaseAssigned,
		events.TypeCommentAdded, events.TypeStatusChanged:
		// Customer cases: see cases.go.
		return e.handleCase(ctx, env)
	case events.TypeIncidentCreated:
		var p events.IncidentCreatedPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return fmt.Errorf("escalation: decode incident.created payload: %w", err)
		}
		return e.start(ctx, triggerFromCreated(env.EntityID, p), false)
	case events.TypeIncidentPriorityElevated:
		var p events.IncidentPriorityElevatedPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return fmt.Errorf("escalation: decode incident.priority_elevated payload: %w", err)
		}
		// An elevation replaces a CRE ladder -- section 7.0 keys its timings
		// on priority -- but never restarts an SRE one, whose clock does not
		// depend on priority. For the SRE engine an elevation matters only
		// when it brings a CRE incident up to a priority that also needs SRE.
		return e.start(ctx, triggerFromElevated(env.EntityID, p), e.cfg.Kind != LadderSRE)
	case events.TypeIncidentCommentAdded:
		if e.cfg.Kind == LadderSRE {
			// Not an SRE acknowledgement: a comment may be a third party
			// triaging, not the engineer who was paged. The SRE ladder stops
			// on an assignee.
			return nil
		}
		var p events.IncidentCommentAddedPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return fmt.Errorf("escalation: decode incident.comment_added payload: %w", err)
		}
		if !p.IsPublic {
			// A work note is an internal jotting, not section 3.0's
			// acknowledgement gesture. Somebody writing one while triaging
			// must not silence their own pager.
			return nil
		}
		return e.cancelBy(ctx, env.EntityID, cancelPublicComment)
	case events.TypeIncidentAssigned:
		if e.cfg.Kind != LadderSRE {
			// Assignment is the SRE ladder's acknowledgement, not the CRE
			// one's: an incident assigned by a dispatcher to somebody who has
			// not yet seen it is not evidence it is being attended.
			return nil
		}
		var p events.IncidentAssignedPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return fmt.Errorf("escalation: decode incident.assigned payload: %w", err)
		}
		err := e.cancelBy(ctx, env.EntityID, cancelAssigned)
		// An engineer taking the incident also answers any Special Ops page
		// raised before the assignment -- open now or still on its way -- so
		// only a later press pages again.
		return errors.Join(err, e.closeSMEPages(ctx, env.EntityID, reportedAt(p.AssignedOn)))
	default:
		return e.cancelBy(ctx, env.EntityID, cancelStateChange)
	}
}

// cancelReason records which of section 3.0's two acknowledgement gestures
// stopped a ladder, so the execution summary says which one it was.
type cancelReason string

const (
	// cancelStateChange is the gesture for a newly reported incident: moving
	// it out of NEW, which is what section 10.0's voice message instructs.
	cancelStateChange cancelReason = "Acknowledged"
	// cancelPublicComment is the gesture for a priority elevation, and the
	// only stop signal such a ladder has.
	cancelPublicComment cancelReason = "Public comment added"
	// cancelAcknowledged is both gestures together, which is what
	// acknowledgement actually means: the incident moved out of NEW and
	// somebody said so where the customer can see it.
	cancelAcknowledged cancelReason = "Acknowledged (status and public comment)"
	// cancelAssigned is the SRE ladder's own gesture: an engineer taking the
	// incident.
	cancelAssigned cancelReason = "Assignee set"
	// cancelEscalatedToSME stops the SaaS SRE chain when the incident is
	// handed to a Special Ops team, whose SME is paged instead (sme.go).
	cancelEscalatedToSME cancelReason = "Escalated to SME"
)

// start expands a trigger into a ladder and schedules it.
//
// replace distinguishes the two triggers. A redelivered incident.created must
// never restart a ladder that is already part-way through — Kafka is
// at-least-once, and restarting would re-dial from LEVEL_0 — so it claims the
// incident with a create-if-absent write and does nothing if one is already
// running. A priority elevation is the opposite: it is a fresh trigger with
// its own, faster timings (section 7.0 keys everything on priority), so it
// deliberately replaces whatever is running, retiring the old ladder's
// outstanding calls first.
func (e *Engine) start(ctx context.Context, t Trigger, replace bool) error {
	if !e.admit(ctx, &t) {
		return nil
	}
	return e.schedule(ctx, t, replace)
}

// admit reports whether this engine runs a ladder for t at all: routing claims
// it, its priority has a clock, and the configuration's trigger gate allows it.
// A case's severity change needs the answer on its own -- a severity this
// ladder does not page stops the running chain (see handleCase).
func (e *Engine) admit(ctx context.Context, t *Trigger) bool {
	if !e.claims(ctx, t) {
		return false
	}
	if _, ok := PolicyFor(e.policies, *t); !ok {
		// Not an error: section 7.0 has no row below P4, so a
		// planning-priority incident legitimately has no ladder. Erroring
		// would dead-letter a valid event.
		slog.InfoContext(ctx, "escalation: no ladder for this priority; skipping",
			"incidentId", t.IncidentID, "priority", t.Priority)
		return false
	}

	// The configuration file's own gate, checked before anything is planned or
	// stored. Skipping is not an error: a deployment that has narrowed which
	// incidents it escalates has said so deliberately, and erroring would
	// dead-letter events it simply does not want to act on.
	gate := e.cfg.Ladder
	if t.Routing.TeamOptional {
		// The routing rule took this incident without a known team on
		// purpose (a monitoring-raised one, say); requireKnownTeam is the
		// guard for everything else.
		gate.Start.RequireKnownTeam = false
	}
	if ok, why := gate.Allows(t.Priority, t.Routing.AssignedCRETeam, string(t.Routing.Shift)); !ok {
		slog.InfoContext(ctx, "escalation: configuration does not escalate this incident; skipping",
			"incidentId", t.IncidentID, "priority", t.Priority,
			"team", t.Routing.AssignedCRETeam, "shift", string(t.Routing.Shift),
			"reason", why)
		return false
	}
	return true
}

// schedule builds an admitted trigger's plan and stores it; see start.
func (e *Engine) schedule(ctx context.Context, t Trigger, replace bool) error {
	// USA_WEEKEND is the one shift whose LEVEL_0 depends on ABT eligibility
	// (R10 has none, R12/R14 do — see RoutingContext.HasNotificationLevel).
	// No publisher populates ABTEligible today: entity-service has no
	// product-to-ABT mapping, so the field arrives false and this shift always
	// takes the R12 branch. A bool cannot distinguish "not eligible" from
	// "nobody told us", so the mis-branch is announced rather than hidden.
	if t.Routing.Ladder != LadderSRE && !t.Routing.abtKnown() {
		// Not a detail: eligibility selects which half of section 5.0's table
		// an incident routes by, so without it the rule is unnamed and the
		// recipients are whatever the roster's fallback tier happens to hold.
		// On USA_WEEKEND it also decides whether LEVEL_0 exists at all.
		slog.WarnContext(ctx, "escalation: no ABT eligibility on this incident; "+
			"the section 5.0 rule cannot be named and routing falls back",
			"incidentId", t.IncidentID, "product", t.Routing.Product,
			"shift", string(t.Routing.Shift),
			"level0Included", t.Routing.HasNotificationLevel())
	}

	plan, err := BuildPlan(ctx, t, e.policies, e.resolver, e.cfg.Channel)
	if err != nil {
		return fmt.Errorf("escalation: build plan for %s: %w", t.IncidentID, err)
	}
	// BuildPlan takes the trigger by value and stamps the matched rule onto
	// its own copy, so the local one here still reports the old fourteen-row
	// derivation -- an LK incident logged rule=R1, a row that shift cannot
	// reach. Adopt the stamped copy so every line below names the row the
	// recipients actually came from.
	t = plan.Trigger
	e.applySafety(ctx, &plan)
	for _, issue := range plan.Issues {
		// Logged without the recipient's email — plan issues carry one in
		// Detail for NO_NUMBER, and this repo does not log recipient
		// addresses (see internal/entity's do() doc comment). The work note
		// is where the per-person detail belongs.
		slog.WarnContext(ctx, "escalation: level cannot be called",
			"incidentId", t.IncidentID, "rule", t.Routing.Rule(),
			"level", issue.Level.String(), "reason", issue.Reason)
	}
	if len(plan.Calls) == 0 && plan.CallsHeld {
		// Setup, not an incident outcome: the lead pool has no numbers yet,
		// and a call-only ladder has nothing else to deliver. No work note.
		return nil
	}
	if len(plan.Calls) == 0 {
		slog.WarnContext(ctx, "escalation: plan has no reachable recipients; nothing scheduled",
			"incidentId", t.IncidentID, "priority", t.Priority, "rule", t.Routing.Rule(),
			"shift", string(t.Routing.Shift), "product", t.Routing.Product, "team", t.Routing.AssignedCRETeam)
		e.writeNote(ctx, plan, nil, nil, nil, "")
		return nil
	}

	// A ladder whose every call is already in the past has nothing left to
	// do, and scheduling it anyway would burst-dial the whole thing on the
	// next tick. That is not hypothetical: this engine's consumer group reads
	// the topic from its beginning the first time it exists (eventbus sets
	// StartOffset to FirstOffset), so the first deployment replays every
	// trigger still in retention — and a redelivery, a DLQ retry or a long
	// consumer outage can all hand it an old trigger later. Every call is an
	// offset from the trigger time on purpose, so a *short* backlog still
	// catches up correctly (the due calls go out on the next tick, at the
	// rung the ladder should be on by now); it is the ladder that has run
	// its whole course before we heard about it that must be dropped.
	if last := plan.Calls[len(plan.Calls)-1]; last.At.Before(e.now()) {
		slog.WarnContext(ctx, "escalation: trigger is older than its whole ladder; not scheduling",
			"incidentId", t.IncidentID, "priority", t.Priority, "trigger", string(t.Kind),
			"triggeredAt", t.At.Format(time.RFC3339), "lastCallAt", last.At.Format(time.RFC3339))
		return nil
	}

	st := LadderState{Plan: plan, Placed: make([]bool, len(plan.Calls))}
	if replace {
		// A replay of the elevation this ladder was built from -- a redelivery, or
		// the same record dead-lettered by the other ladder's engine and read back
		// by this one's DLQ consumer -- must not restart it: a fresh state marks
		// every call unplaced, and those already due would be dialled again.
		running, found, err := e.store.Get(ctx, t.IncidentID)
		if err != nil {
			return fmt.Errorf("escalation: read running ladder for %s: %w", t.IncidentID, err)
		}
		if found && running.Cancelled == nil && sameSeverity(running.Plan.Trigger, t) {
			// A case's severity change carries no timestamp, so a replay is
			// recognised by the chain already running at that severity.
			slog.InfoContext(ctx, "escalation: chain already runs at this severity; ignoring the replay",
				"incidentId", t.IncidentID, "priority", t.Priority)
			return nil
		}
		if found && sameElevation(running.Plan.Trigger, t) {
			slog.InfoContext(ctx, "escalation: elevation already applied to the running ladder; ignoring the replay",
				"incidentId", t.IncidentID, "priority", t.Priority, "elevatedAt", t.At.Format(time.RFC3339))
			return nil
		}
		previous, hadOne, err := e.retireRunning(ctx, t.IncidentID)
		if err != nil {
			return err
		}
		// An acknowledgement is about the incident, not about the ladder that
		// happened to be climbing when it arrived. Under RequireBothGestures a
		// responder's two gestures can straddle an elevation -- they move it
		// out of NEW, somebody raises the priority, then they comment -- and a
		// fresh state would forget the first half. The move out of NEW cannot
		// happen twice, so the second gesture would never complete the pair
		// and the replacement ladder would keep climbing past an incident
		// somebody had already picked up.
		if hadOne && !t.isCase() {
			st.SawStateChange = previous.SawStateChange
			st.SawPublicComment = previous.SawPublicComment
		}
		// A case's new chain starts fresh -- only gestures after the change
		// count -- except that an assignee already on the case stays assigned.
		if err := e.seedCaseAssignment(ctx, &st); err != nil {
			return err
		}
		if err := e.store.Save(ctx, t.IncidentID, st); err != nil {
			return fmt.Errorf("escalation: replace ladder for %s: %w", t.IncidentID, err)
		}
	} else {
		if err := e.seedCaseAssignment(ctx, &st); err != nil {
			return err
		}
		created, err := e.store.Create(ctx, t.IncidentID, st)
		if err != nil {
			return fmt.Errorf("escalation: claim ladder for %s: %w", t.IncidentID, err)
		}
		if !created {
			return e.resumeRunning(ctx, t.IncidentID)
		}
	}

	// Seed a wake for every call. If this fails part-way the ladder is left
	// short of wakes, and Tick only ever looks at wakes that exist -- so the
	// missing calls would simply never be made, silently. The redelivery of
	// this record is what repairs that, through resumeRunning.
	seeded, err := e.seedPendingWakes(ctx, t.IncidentID, st)
	if err != nil {
		return err
	}
	if seeded != len(plan.Calls) {
		// A ladder written moments ago has nothing placed and nothing failed,
		// so this cannot happen. If it ever does, the ladder is short of calls
		// and somebody needs to know which incident.
		slog.WarnContext(ctx, "escalation: ladder scheduled with fewer wakes than calls",
			"incidentId", t.IncidentID, "seeded", seeded, "calls", len(plan.Calls))
	}
	// One line that answers "which ladder, and by which path" without anyone
	// re-deriving section 5.0's table from four fields by hand. levels is the
	// rungs this incident will actually climb, in order, which is the part
	// that differs between rules — a USA_WEEKEND ABT incident has no LEVEL_0
	// (R10) while its IAM counterpart does (R12).
	slog.InfoContext(ctx, "escalation: ladder scheduled",
		"incidentId", t.IncidentID, "priority", t.Priority, "trigger", string(t.Kind),
		"rule", t.Routing.Rule(), "shift", string(t.Routing.Shift),
		"product", t.Routing.Product, "team", t.Routing.AssignedCRETeam,
		"abtEligible", t.Routing.ABTEligibility(),
		"ladder", ladderName(t.Routing.Ladder),
		"levels", plan.LevelsClimbed(), "calls", len(plan.Calls),
		"lastCallAt", plan.Calls[len(plan.Calls)-1].At.Format(time.RFC3339))
	return nil
}

// claims decides whether this engine's ladder runs for a trigger, and stamps
// the trigger with that ladder and the routing rule that put it there.
//
// The decision is the configuration's routing rules (routing.go), not code:
// the incident's team family, contact type and priority are matched against
// them, and this engine claims the incident when a rule names its ladder.
//
// One thing stays in code because it is a property of the SRE ladder rather
// than of routing: its clock does not depend on priority, so an elevation
// matters to it only through a rule that conditions on priority (a CRE
// incident reaching P0). Anything else would restart a ladder for nothing.
func (e *Engine) claims(ctx context.Context, t *Trigger) bool {
	family := e.teamFamily(ctx, *t)
	key := LadderKeyCRE
	if e.cfg.Kind == LadderSRE {
		key = LadderKeySRE
	}
	rule, ok := e.cfg.Routing.Match(RouteInput{
		Record: t.recordKind(), Team: family, ContactType: t.Routing.ContactType, Priority: t.Priority,
	}, key)
	if !ok {
		return false
	}
	if e.cfg.Kind == LadderSRE && (t.Kind == TriggerPriorityElevated || t.Kind == TriggerSeverityChanged) &&
		len(rule.When.Priority) == 0 {
		return false
	}
	t.Routing.Ladder = e.cfg.Kind
	t.Routing.CallOnLeave = e.cfg.Ladder.OnLeave.Calls()
	t.Routing.RouteRule = rule.Name
	t.Routing.TeamOptional = rule.AdmitsNoTeam()
	if e.cfg.Kind == LadderSRE {
		t.Routing.Rota = e.sreRota(ctx, *t)
	}
	slog.InfoContext(ctx, "escalation: routing put the incident on this ladder",
		"incidentId", t.IncidentID, "ladder", key, "rule", rule.Name,
		"teamFamily", family, "contactType", t.Routing.ContactType, "priority", t.Priority,
		"rota", t.Routing.Rota)
	return true
}

// sreRota asks the resolver which SRE rota pages the incident: SaaS or IaaS.
// A resolver that cannot say, or a failure to ask, leaves it empty -- the
// resolver then decides per rung, and the chain goes unnamed.
func (e *Engine) sreRota(ctx context.Context, t Trigger) string {
	r, ok := e.resolver.(SRERotaResolver)
	if !ok {
		return ""
	}
	rota, err := r.SRERota(ctx, t.Routing)
	if err != nil {
		slog.WarnContext(ctx, "escalation: could not tell which SRE rota pages this incident",
			"incidentId", t.IncidentID, "team", t.Routing.AssignedCRETeam, "err", err)
		return ""
	}
	return rota
}

// teamFamily asks the resolver which family the incident's team belongs to.
// A resolver that cannot say falls back to the ladder classifier, and then to
// "cre" for any named team -- the ladder every incident climbed before the
// SRE one existed. A failure to ask is treated the same way.
func (e *Engine) teamFamily(ctx context.Context, t Trigger) string {
	if r, ok := e.resolver.(TeamFamilyResolver); ok {
		family, err := r.TeamFamily(ctx, t.Routing)
		if err == nil {
			return family
		}
		slog.WarnContext(ctx, "escalation: could not tell the incident's team family; routing it as CRE",
			"incidentId", t.IncidentID, "team", t.Routing.AssignedCRETeam, "err", err)
	} else if e.classify(ctx, t) == LadderSRE {
		return TeamFamilySRE
	}
	if strings.TrimSpace(t.Routing.AssignedCRETeam) == "" {
		return TeamFamilyNone
	}
	return TeamFamilyCRE
}

// classify asks the resolver which ladder this incident climbs. A resolver
// that cannot tell, or a failure to ask, leaves it on the CRE ladder: that is
// the one every incident climbed before the SRE ladder existed, and an
// incident still gets escalated rather than dropped.
func (e *Engine) classify(ctx context.Context, t Trigger) Ladder {
	c, ok := e.resolver.(LadderClassifier)
	if !ok {
		return LadderCRE
	}
	ladder, err := c.LadderFor(ctx, t.Routing)
	if err != nil {
		slog.WarnContext(ctx, "escalation: could not tell which ladder this incident climbs; using CRE",
			"incidentId", t.IncidentID, "team", t.Routing.AssignedCRETeam, "err", err)
		return LadderCRE
	}
	return ladder
}

// sameSeverity reports whether t is a case severity change to the severity the
// running chain already pages at: a redelivery, since a real change always
// moves the severity.
func sameSeverity(running, t Trigger) bool {
	return t.Kind == TriggerSeverityChanged && running.isCase() &&
		NormalisePriority(running.Priority) == NormalisePriority(t.Priority)
}

// sameElevation reports whether running was built from the very elevation t
// carries: the same trigger kind, instant and priority.
func sameElevation(running, t Trigger) bool {
	return running.Kind == TriggerPriorityElevated && t.Kind == TriggerPriorityElevated &&
		running.At.Equal(t.At) && NormalisePriority(running.Priority) == NormalisePriority(t.Priority)
}

func ladderName(l Ladder) string {
	if l == LadderSRE {
		return "SRE"
	}
	return "CRE"
}

// seedPendingWakes writes a wake entry for every call still waiting to be
// made, and reports how many it wrote.
//
// The predicate is pendingMembers': not yet placed, and not permanently
// failed. ZADD with an unchanged score is idempotent, so a member that is
// already there costs nothing -- which is what makes this safe to run over a
// ladder that is only partly seeded, without first asking which wakes exist.
func (e *Engine) seedPendingWakes(ctx context.Context, incidentID string, st LadderState) (int, error) {
	seeded := 0
	for i, placed := range st.Placed {
		if placed || st.failure(i) != "" || i >= len(st.Plan.Calls) {
			continue
		}
		if err := e.store.AddWake(ctx, wakeMember(incidentID, i), st.Plan.Calls[i].At); err != nil {
			return seeded, fmt.Errorf("escalation: schedule call %d for %s: %w", i, incidentID, err)
		}
		seeded++
	}
	return seeded, nil
}

// resumeRunning handles a trigger for an incident that already has a ladder.
//
// Create claims the incident, and the wakes are seeded after it. Those are two
// writes, and a failure between them leaves a ladder whose later calls have no
// wake to fire them -- which Tick cannot detect, because it only walks wakes
// that exist. Returning here on the strength of "someone already owns this"
// would make that state permanent.
//
// So the redelivery re-seeds the running ladder's own pending calls rather
// than the incoming trigger's: whatever is stored is the ladder that is
// actually climbing, and the duplicate trigger has no authority to re-plan it.
// Re-seeding is idempotent, so the common case -- a plain duplicate, nothing
// broken -- costs one pass and changes nothing.
func (e *Engine) resumeRunning(ctx context.Context, incidentID string) error {
	st, found, err := e.store.Get(ctx, incidentID)
	if err != nil {
		return fmt.Errorf("escalation: load running ladder for %s: %w", incidentID, err)
	}
	if !found {
		// Completed, cancelled or replaced between Create and this read. There
		// is no ladder to repair and nothing to schedule.
		slog.InfoContext(ctx, "escalation: duplicate trigger for a ladder that has since finished",
			"incidentId", incidentID)
		return nil
	}
	seeded, err := e.seedPendingWakes(ctx, incidentID, st)
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "escalation: ladder already running; re-seeded its pending wakes",
		"incidentId", incidentID, "pending", seeded, "reachedLevel", st.ReachedLevel())
	return nil
}

// retireRunning drops any outstanding wake entries for an incident, used when
// an elevation replaces a running ladder. The old ladder's state is not
// deleted here — Save overwrites it immediately after — but it is returned,
// because some of what it recorded outlives the ladder that recorded it. See
// the acknowledgement flags in start.
func (e *Engine) retireRunning(ctx context.Context, incidentID string) (LadderState, bool, error) {
	st, found, err := e.store.Get(ctx, incidentID)
	if err != nil {
		return LadderState{}, false, fmt.Errorf("escalation: load running ladder for %s: %w", incidentID, err)
	}
	if !found {
		return LadderState{}, false, nil
	}
	if err := e.store.RemoveWakes(ctx, pendingMembers(incidentID, st)...); err != nil {
		return LadderState{}, false, fmt.Errorf("escalation: retire running ladder for %s: %w", incidentID, err)
	}
	return st, true, nil
}

// cancelBy stops a running ladder because the incident was acknowledged, by
// whichever of section 3.0's two gestures reason names.
//
// Both gestures stop ANY running ladder, not only the one their own trigger
// started. Section 3.0 pairs a gesture with each trigger — status change for a
// new incident, public comment for an elevation — but treating them as
// mutually exclusive would mean a responder who commented on a newly reported
// incident, rather than moving it to Work In Progress, keeps being called
// while visibly working on it. Both are unambiguous evidence the incident is
// being attended, which is what the ladder exists to provoke, so either one
// ends it. This is deliberately slightly broader than the document's literal
// pairing.
//
// Order matters. The remaining wake entries are dropped first — stopping the
// calls is the whole point, and it is idempotent — then the work note is
// written, then the state is deleted. A failed work note therefore retries
// with the state still present (and Cancelled already set, so the drop is not
// repeated), rather than losing the summary.
func (e *Engine) cancelBy(ctx context.Context, incidentID string, reason cancelReason) error {
	st, found, err := e.store.Get(ctx, incidentID)
	if err != nil {
		return fmt.Errorf("escalation: load ladder for %s: %w", incidentID, err)
	}
	if !found {
		// The common case: an incident acknowledged without a ladder ever
		// having run, or one already finished.
		return nil
	}

	// Acknowledgement is BOTH gestures, not either: the incident moved out of
	// NEW and a public comment written. Moving the status alone is what a
	// dispatcher does while triaging a queue, and it would otherwise silence
	// the pager for an incident nobody had actually picked up -- which is the
	// case this rule exists for. The card and the voice message have always
	// asked for both; only the engine disagreed.
	//
	// The two arrive as separate events in either order, so each is recorded
	// and the ladder keeps climbing until both are in.
	//
	// CRE only. The SRE ladder's acknowledgement is one gesture, an engineer
	// assigned (or the incident leaving NEW); a public comment never reaches
	// here for it (Handle drops it), and treating an assignee as half of a
	// CRE pair would leave an SRE ladder climbing for ever.
	if st.Cancelled == nil && e.cfg.Kind != LadderSRE && e.cfg.Ladder.RequireBothGestures() {
		switch reason {
		case cancelStateChange:
			st.SawStateChange = true
		case cancelPublicComment:
			st.SawPublicComment = true
		}
		if !(st.SawStateChange && st.SawPublicComment) {
			missing := "a public comment"
			if !st.SawStateChange {
				missing = "a move out of NEW"
			}
			if err := e.store.Save(ctx, incidentID, st); err != nil {
				return fmt.Errorf("escalation: record acknowledgement for %s: %w", incidentID, err)
			}
			slog.InfoContext(ctx, "escalation: half acknowledged; the ladder keeps climbing",
				"incidentId", incidentID, "saw", string(reason), "stillNeeds", missing,
				"reachedLevel", st.ReachedLevel())
			return nil
		}
		reason = cancelAcknowledged
	}
	return e.stopLadder(ctx, incidentID, st, reason)
}

// stopLadder ends a running ladder for reason: marks it cancelled, drops its
// pending calls, writes the summary and deletes the state. Order matters; see
// cancelBy.
func (e *Engine) stopLadder(ctx context.Context, incidentID string, st LadderState, reason cancelReason) error {
	if st.Cancelled == nil {
		now := time.Now()
		st.Cancelled = &now
		st.CancelReason = string(reason)
		if err := e.store.Save(ctx, incidentID, st); err != nil {
			return fmt.Errorf("escalation: mark ladder cancelled for %s: %w", incidentID, err)
		}
		pending := pendingMembers(incidentID, st)
		if err := e.store.RemoveWakes(ctx, pending...); err != nil {
			return fmt.Errorf("escalation: stop ladder for %s: %w", incidentID, err)
		}
		slog.InfoContext(ctx, "escalation: ladder cancelled",
			"incidentId", incidentID, "reason", string(reason),
			"rule", st.Plan.Trigger.Routing.Rule(), "priority", st.Plan.Trigger.Priority,
			"reachedLevel", st.ReachedLevel(), "placedCalls", st.PlacedCount(),
			"cancelledCalls", len(pending))
	}

	e.writeNote(ctx, st.Plan, st.Placed, st.Failed, st.Cancelled, st.CancelReason)
	return e.store.Delete(ctx, incidentID)
}

// Tick places every call that has come due. Mirrors slaengine.Engine.Tick: one
// scan per tick, each due entry handled independently so one failure does not
// block the rest.
func (e *Engine) Tick(ctx context.Context, now time.Time) error {
	members, err := e.store.DueMembers(ctx, now)
	if err != nil {
		return fmt.Errorf("escalation: scan due calls: %w", err)
	}
	var errs []error
	for _, member := range members {
		if err := e.processDue(ctx, member); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// processDue handles one due call.
//
// The call is placed, then recorded, then its wake entry dropped — in that
// order, so a crash or a Redis failure after dialling leaves the entry in
// place and the call is repeated next tick. That is the deliberate direction
// to fail in: this is a paging system, and a duplicate call to the same
// on-call engineer costs far less than a page that never happens. It is the
// same trade-off slaengine.processDueMember documents for its Chat send.
func (e *Engine) processDue(ctx context.Context, member string) error {
	incidentID, index, ok := parseWakeMember(member)
	if !ok {
		slog.ErrorContext(ctx, "escalation: malformed wake member, dropping", "member", member)
		return e.store.RemoveWakes(ctx, member)
	}

	st, found, err := e.store.Get(ctx, incidentID)
	if err != nil {
		return fmt.Errorf("escalation: load ladder for %s: %w", incidentID, err)
	}
	if !found || index >= len(st.Placed) {
		// The ladder was cancelled, completed or replaced and this entry is a
		// leftover. Dropping it is the correct outcome.
		return e.store.RemoveWakes(ctx, member)
	}
	if st.Cancelled != nil || st.Placed[index] || st.failure(index) != "" {
		return e.store.RemoveWakes(ctx, member)
	}

	// Two replicas tick independently (a rolling restart always has two for
	// a moment), and both see the same due call. Only the one that claims it
	// dials. The claim expires on its own, so a replica that dies here delays
	// the call by at most callClaimTTL rather than losing it.
	// Keyed by the chain as well as the call: a replacement chain (a severity
	// change, an elevation) reuses the same call slots, and must not wait out
	// the old chain's claim.
	claim := fmt.Sprintf("%s@%d", member, st.Plan.Trigger.At.UnixNano())
	claimed, err := e.store.ClaimCall(ctx, claim, callClaimTTL)
	if err != nil {
		return fmt.Errorf("escalation: claim due call for %s: %w", incidentID, err)
	}
	if !claimed {
		return nil
	}

	call := st.Plan.Calls[index]
	var failure string
	if err := e.place(ctx, st.Plan, call); err != nil {
		if !isPermanent(err) {
			// Transient — leave the wake entry and give the claim back, so the
			// next tick retries.
			if rerr := e.store.ReleaseCall(ctx, claim); rerr != nil {
				slog.WarnContext(ctx, "escalation: could not release a failed call's claim; it retries when the claim expires",
					"incidentId", incidentID, "err", rerr)
			}
			return fmt.Errorf("escalation: place %s call for %s: %w", call.Level, incidentID, err)
		}
		// The provider rejected the request itself; trying again with the
		// same number and the same document cannot succeed. Record why, so
		// the work note says this person was not reached and the ladder can
		// still complete, then treat it like a placed call for scheduling.
		slog.ErrorContext(ctx, "escalation: call rejected by the provider; not retrying",
			"incidentId", incidentID, "rule", st.Plan.Trigger.Routing.Rule(),
			"level", call.Level.String(), "attempt", call.Ordinal,
			"to", maskPhone(call.Recipient.Phone), "reason", permanentReason(err))
		failure = permanentReason(err)
	}

	// Re-read before writing, and carry over only what this goroutine owns.
	//
	// e.place above is an HTTP call that can take seconds, and the consumer
	// goroutine is live throughout: an acknowledgement can arrive and write
	// SawStateChange, or Cancelled, or delete the ladder outright. Saving the
	// copy read before the call would put all of that back -- losing a half
	// acknowledgement, so the ladder demands a gesture that already arrived,
	// or resurrecting a ladder somebody has already stopped and calling the
	// next rung.
	//
	// This narrows the window to the gap between this read and the save
	// rather than closing it: there is no compare-and-set here, and a proper
	// fix is a Lua script that does both in one round trip. The remaining gap
	// is microseconds against seconds, and it fails in the recoverable
	// direction -- a placed call is not recorded, so it is placed again.
	fresh, stillRunning, err := e.store.Get(ctx, incidentID)
	if err != nil {
		return fmt.Errorf("escalation: reload ladder for %s: %w", incidentID, err)
	}
	if !stillRunning || fresh.Cancelled != nil || index >= len(fresh.Placed) {
		// Acknowledged, replaced or completed while the call was in flight.
		// The call did go out, but there is nothing left to record it on.
		return e.store.RemoveWakes(ctx, member)
	}
	if failure != "" {
		fresh.setFailure(index, failure)
	} else {
		fresh.Placed[index] = true
	}
	st = fresh

	if err := e.store.Save(ctx, incidentID, st); err != nil {
		return fmt.Errorf("escalation: record placed call for %s: %w", incidentID, err)
	}
	if err := e.store.RemoveWakes(ctx, member); err != nil {
		return fmt.Errorf("escalation: clear placed call for %s: %w", incidentID, err)
	}

	if st.AllSettled() {
		// The ladder ran to its end without anyone acknowledging. Record what
		// happened and stop tracking it.
		e.writeNote(ctx, st.Plan, st.Placed, st.Failed, nil, "")
		e.postUnanswered(ctx, st)
		slog.WarnContext(ctx, "escalation: ladder exhausted without acknowledgement",
			"incidentId", incidentID, "priority", st.Plan.Trigger.Priority,
			"rule", st.Plan.Trigger.Routing.Rule(), "reachedLevel", st.ReachedLevel(),
			"placedCalls", st.PlacedCount())
		return e.store.Delete(ctx, incidentID)
	}
	return nil
}

// place notifies one recipient over every configured channel, or logs what it
// would have done when sending is disabled.
//
// Two log lines, not one, and both matter when something goes wrong at 3am.
// The first is written BEFORE the attempt, so one that hangs or crashes the
// process still leaves a record that this rung was about to page someone. The
// second is written after a channel accepts it, and carries the provider's own
// handle where there is one — a Twilio call sid is what an operator searches
// the console by to find out whether it actually rang.
//
// Every channel is attempted even if an earlier one fails, and the errors are
// joined: a chat webhook being down must not stop the phone ringing, and a
// phone failing must not cost the room its sight of the escalation.
func (e *Engine) place(ctx context.Context, plan Plan, call PlannedCall) error {
	if plan.Trigger.Routing.Ladder == LadderSME {
		// An SME ladder rides the SRE engine's ticker but pages over the
		// SME's own channel and Chat space.
		return e.placeVia(ctx, plan, call, e.smeNotifiersFor(plan.Trigger.Routing.SMETeam), e.cfg.SME.Channel)
	}
	return e.placeVia(ctx, plan, call, e.notifiersFor(plan), e.cfg.Channel)
}

// placeVia is place over the given notifiers; the SME page has its own.
func (e *Engine) placeVia(ctx context.Context, plan Plan, call PlannedCall, notifiers []notifier, channel Channel) error {
	t := plan.Trigger
	if !e.cfg.CallSendingEnabled {
		slog.InfoContext(ctx, "escalation: sending disabled (CALL_SENDING_ENABLED=false); not notifying",
			"incidentId", t.IncidentID, "rule", t.Routing.Rule(), "priority", t.Priority,
			"level", call.Level.String(), "attempt", call.Ordinal,
			"channel", string(channel), "to", maskPhone(call.Recipient.Phone))
		return nil
	}
	if len(notifiers) == 0 {
		// Configured for a channel whose client was never constructed. Not an
		// error to retry — no tick will fix it — but never silent either.
		slog.ErrorContext(ctx, "escalation: no notifier configured for this channel; nobody was contacted",
			"incidentId", t.IncidentID, "channel", string(channel),
			"level", call.Level.String(), "attempt", call.Ordinal)
		return nil
	}

	slog.InfoContext(ctx, "escalation: notifying",
		"incidentId", t.IncidentID, "rule", t.Routing.Rule(), "priority", t.Priority,
		"level", call.Level.String(), "attempt", call.Ordinal,
		"channel", string(channel), "to", maskPhone(call.Recipient.Phone),
		"message", messageKind(e.cfg.UseSSML))

	var errs []error
	for _, n := range notifiers {
		if call.HoldCall && n.Channel() == ChannelCall {
			continue
		}
		delivered, err := n.Deliver(ctx, plan, call)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", n.Channel(), err))
			continue
		}
		slog.InfoContext(ctx, "escalation: ALERT TRIGGERED",
			"incidentId", t.IncidentID, "rule", t.Routing.Rule(), "priority", t.Priority,
			"level", call.Level.String(), "attempt", call.Ordinal,
			"recipient", call.Recipient.Name, "to", maskPhone(call.Recipient.Phone),
			"channel", string(delivered.Channel), "ref", delivered.Ref,
			"status", delivered.Status)
	}

	// Record the call for the evening pairing's fairness rule, whatever the
	// channel was -- somebody reached by a card has been reached. Best-effort
	// on purpose: an unrecorded call makes the next pairing slightly less
	// fair, which is not worth failing a page over, and the error is a log
	// line rather than an entry in errs so it can never cause a retry that
	// dials somebody a second time.
	if e.store != nil && call.Recipient.Email != "" {
		if err := e.store.MarkCalled(ctx, call.Recipient.Email, time.Now()); err != nil {
			slog.WarnContext(ctx, "escalation: could not record who was called; the next pairing may repeat somebody",
				"incidentId", t.IncidentID, "err", err)
		}
	}
	return errors.Join(errs...)
}

// messageKind names which voice document a call carried, so a log line says
// whether the SSML path or the plain one was exercised.
func messageKind(ssml bool) string {
	if ssml {
		return "ssml"
	}
	return "plain"
}

// writeNote PATCHes the execution summary onto the incident (section 11.0).
// With no entity-service client configured the summary is logged instead, so a
// deployment without one still runs the ladder rather than failing every
// record.
func (e *Engine) writeNote(ctx context.Context, plan Plan, placed []bool, failed []string, cancelledAt *time.Time, reason string) {
	note := plan.WorkNote(placed, failed, cancelledAt, reason)
	if e.notes == nil {
		slog.InfoContext(ctx, "escalation: no incident-notes client configured; execution summary not written back",
			"incidentId", plan.Trigger.IncidentID)
		return
	}
	write := e.notes.AppendWorkNote
	if plan.Trigger.isCase() {
		write = e.notes.AppendCaseWorkNote
	}
	if err := write(ctx, plan.Trigger.IncidentID, note); err != nil {
		// Logged, never returned. This used to propagate, and a record whose
		// summary could not be written was retried three times and then
		// dead-lettered -- an incident that had genuinely been handled,
		// abandoned because the note about it would not save. PATCH
		// /incidents is 503 on a Postgres deployment, so that was every
		// record, including ones with nobody to call, which is how it was
		// found.
		//
		// The summary is a record of work already done: the calls are placed,
		// the cards are posted, the state is settled. Losing the record is a
		// loss; discarding the event is worse, and retrying cannot help,
		// because whatever refused the write will refuse it again.
		slog.ErrorContext(ctx, "escalation: could not write the execution summary; it is lost for this incident",
			"incidentId", plan.Trigger.IncidentID, "error", err)
	}
}

// RunTicker calls Tick every interval until ctx is done, from its own
// goroutine (see cmd/server/main.go). A failed tick is logged, not fatal — the
// next tick retries whatever was due.
func (e *Engine) RunTicker(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.Tick(ctx, time.Now()); err != nil {
				slog.ErrorContext(ctx, "escalation: tick failed", "err", err)
			}
		}
	}
}

// pendingMembers lists the wake members for calls that have not been placed.
func pendingMembers(incidentID string, st LadderState) []string {
	var out []string
	for i, done := range st.Placed {
		if !done && st.failure(i) == "" {
			out = append(out, wakeMember(incidentID, i))
		}
	}
	return out
}

// isPermanent reports whether a call error is one that retrying cannot fix:
// the provider accepted the request and rejected its content. Anything else —
// a network failure, a 5xx, a timeout — is transient and stays scheduled.
// undeliverable is a delivery that no retry can make succeed: the
// configuration has nowhere to send it. Permanent, so the engine records the
// reason against the call and the ladder carries on, instead of retrying
// every tick.
type undeliverable struct{ reason, detail string }

func (u *undeliverable) Error() string { return u.detail }

func isPermanent(err error) bool {
	var u *undeliverable
	if errors.As(err, &u) {
		return true
	}
	var upstream *apierror.Error
	if errors.As(err, &upstream) {
		return upstream.StatusCode >= 400 && upstream.StatusCode < 500
	}
	return false
}

// twilioCodePattern finds the provider's own error code in a rejection body.
//
// Scanned for rather than JSON-decoded on purpose: the body is truncated to a
// fixed budget before it reaches here (see the call client's maxErrBody), so a
// long message — and Twilio's are long, they name the offending number and
// link its documentation — leaves the JSON unparsable and took the code down
// with it. A real rejection reported only as "REJECTED_400" sent this
// investigation to the wrong error entirely: 21219 (destination unverified)
// and 21210 (source number not on the account) are the same status and
// completely different fixes.
var twilioCodePattern = regexp.MustCompile(`"code"\s*:\s*(\d+)`)

// permanentReason renders a rejection for the log line and the work note:
// the provider's own status and, where it gives one, its error code — never
// the message, which echoes the phone number back into places this service
// keeps numbers out of.
func permanentReason(err error) string {
	var u *undeliverable
	if errors.As(err, &u) {
		return u.reason
	}
	var upstream *apierror.Error
	if !errors.As(err, &upstream) {
		return "REJECTED"
	}
	if m := twilioCodePattern.FindStringSubmatch(upstream.Body); m != nil {
		return fmt.Sprintf("REJECTED_%d_%s", upstream.StatusCode, m[1])
	}
	return fmt.Sprintf("REJECTED_%d", upstream.StatusCode)
}

// maskPhone keeps only the last four digits, matching dispatch.maskPhone's own
// shape (unexported there, and dispatch importing this package would be the
// wrong direction).
func maskPhone(phone string) string {
	if len(phone) <= 4 {
		return "****"
	}
	return "********" + phone[len(phone)-4:]
}

// triggerFromCreated builds a new-incident trigger from its event payload.
//
// The effective shift is derived from the report time rather than carried in
// the payload: section 5.0 takes it from ServiceNow's on-call schedule, which
// entity-service cannot read either, and deriving it here keeps one definition
// of the boundaries (see ShiftAt).
func triggerFromCreated(incidentID string, p events.IncidentCreatedPayload) Trigger {
	// Resolved once, not once per use: when the payload carries no timestamp
	// both uses fall back to time.Now(), and two separate calls would read
	// two different instants — which across a shift boundary would schedule
	// the ladder against one shift and route it by another.
	at := reportedAt(p.ReportedAt)
	return Trigger{
		IncidentID: incidentID,
		Number:     p.Number,
		WSO2CaseID: p.WSO2CaseID,
		Priority:   p.Priority,
		Title:      p.Title,
		Account:    p.Account,
		Team:       p.Team,
		Kind:       TriggerNewIncident,
		At:         at,
		Routing: RoutingContext{
			Product:         p.Product,
			ABTEligible:     p.ABTEligible,
			AssignedCRETeam: p.Team,
			ContactType:     p.ContactType,
			Shift:           ShiftAt(at),
			At:              at,
		},
	}
}

// triggerFromElevated builds a priority-elevation trigger from its event
// payload.
func triggerFromElevated(incidentID string, p events.IncidentPriorityElevatedPayload) Trigger {
	at := reportedAt(p.ElevatedAt)
	return Trigger{
		IncidentID: incidentID,
		Number:     p.Number,
		WSO2CaseID: p.WSO2CaseID,
		Priority:   p.NewPriority,
		Title:      p.Title,
		Account:    p.Account,
		Team:       p.Team,
		Kind:       TriggerPriorityElevated,
		At:         at,
		Routing: RoutingContext{
			Product:         p.Product,
			ABTEligible:     p.ABTEligible,
			AssignedCRETeam: p.Team,
			Shift:           ShiftAt(at),
			At:              at,
		},
	}
}

// reportedAt parses the trigger time a payload carries, falling back to now.
//
// The fallback matters for the ladder's honesty: every call is an offset from
// the trigger, so consuming a backlogged record must not shift the whole
// ladder later than section 7.0 intends. When the publisher tells us when the
// incident was actually reported, that is what the offsets are measured from.
func reportedAt(raw string) time.Time {
	if raw == "" {
		return time.Now()
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Now()
	}
	return t
}

// applySafety trims a freshly built plan to what the configuration file says
// one ladder may spend. It runs before anything is stored, so a capped ladder
// is capped for its whole life rather than only until the next restart.
//
// Each cap records a PlanIssue rather than silently shrinking the plan. A
// ladder that reaches fewer people than the rules say it should is a fact
// somebody needs on the work note, not a quiet saving.
func (e *Engine) applySafety(ctx context.Context, plan *Plan) {
	s := e.cfg.Ladder.Safety

	if top, capped := e.cfg.Ladder.CapLevel(); capped {
		kept := plan.Calls[:0]
		var dropped int
		for _, c := range plan.Calls {
			if c.Level <= top {
				kept = append(kept, c)
				continue
			}
			dropped++
		}
		if dropped > 0 {
			plan.Calls = kept
			plan.Issues = append(plan.Issues, PlanIssue{
				Level:  top,
				Reason: "LEVEL_CAPPED",
			})
			slog.WarnContext(ctx, "escalation: ladder capped by configuration; the rungs above it will never be reached",
				"incidentId", plan.Trigger.IncidentID, "maxLevel", top.String(), "callsDropped", dropped)
		}
	}

	// Numbers are filtered rather than the calls being left to fail: an
	// unlisted recipient is not an error, it is a deployment saying "not this
	// person". The number itself is never logged.
	if len(s.AllowedNumbers) > 0 {
		kept := plan.Calls[:0]
		var dropped int
		for _, c := range plan.Calls {
			if e.cfg.Ladder.Dialable(c.Recipient.Phone) {
				kept = append(kept, c)
				continue
			}
			dropped++
		}
		if dropped > 0 {
			plan.Calls = kept
			plan.Issues = append(plan.Issues, PlanIssue{Reason: "NUMBER_NOT_ALLOWED"})
			slog.WarnContext(ctx, "escalation: recipients dropped by safety.allowedNumbers",
				"incidentId", plan.Trigger.IncidentID, "callsDropped", dropped)
		}
	}

	// No calls at all until the lead pool can be called: see
	// Safety.CallWithoutVerifiedLeads. Log only -- nothing on the work note.
	if e.cfg.Channel.Uses(ChannelCall) && !s.CallWithoutVerifiedLeads {
		if missing, err := e.unverifiedLeads(ctx); err != nil || missing > 0 {
			plan.CallsHeld = true
			if e.cfg.Channel == ChannelCall {
				plan.Calls = nil
			} else {
				for i := range plan.Calls {
					plan.Calls[i].HoldCall = true
				}
			}
			slog.WarnContext(ctx, "escalation: calls held -- the ABT lead pool is not verified with phone numbers",
				"incidentId", plan.Trigger.IncidentID, "leadsWithoutNumber", missing, "err", err)
		}
	}

	// The heads are not paged for an incident nobody below them could be
	// reached about: see Safety.CallHeadsWithoutLowerTiers. Checked after the
	// number allowlist, so a lower-tier number this deployment may not dial
	// does not count as reachable. Log only -- nothing on the work note.
	if e.cfg.Channel.Uses(ChannelCall) && !plan.CallsHeld && !s.CallHeadsWithoutLowerTiers && !lowerTiersDialable(plan.Calls) {
		kept := plan.Calls[:0]
		var held int
		for _, c := range plan.Calls {
			if c.Level < Level3 {
				kept = append(kept, c)
				continue
			}
			held++
			if e.cfg.Channel == ChannelCall {
				// A call-only entry is nothing but the call.
				continue
			}
			// With both channels the entry still carries the rung's chat card.
			c.HoldCall = true
			kept = append(kept, c)
		}
		plan.Calls = kept
		if held > 0 {
			slog.WarnContext(ctx, "escalation: heads' calls held -- nobody on LEVEL_0..LEVEL_2 has a number to call",
				"incidentId", plan.Trigger.IncidentID, "callsHeld", held)
		}
	}

	// The call cap is deliberately last and deliberately truncating rather
	// than refusing: by this point the earlier rungs are the ones worth
	// keeping, and a ladder that reaches its first responders is better than
	// one that reaches nobody because its top rung resolved to forty people.
	if s.MaxCallsPerLadder > 0 && len(plan.Calls) > s.MaxCallsPerLadder {
		dropped := len(plan.Calls) - s.MaxCallsPerLadder
		plan.Calls = plan.Calls[:s.MaxCallsPerLadder]
		plan.Issues = append(plan.Issues, PlanIssue{Reason: "CALL_CAP_REACHED"})
		slog.WarnContext(ctx, "escalation: plan truncated by safety.maxCallsPerLadder; "+
			"the rota may have grown, or the cap may be too low",
			"incidentId", plan.Trigger.IncidentID,
			"cap", s.MaxCallsPerLadder, "callsDropped", dropped)
	}
}

// unverifiedLeads counts the leads in the resolver's lead pool without a
// dialable (E.164) number, naming them in the log so whoever owns the data
// knows who to ask. A resolver that cannot name a pool is not checked. An
// empty pool is not verified: there is nobody for the lead tiers to call.
func (e *Engine) unverifiedLeads(ctx context.Context) (int, error) {
	lp, ok := e.resolver.(LeadPoolResolver)
	if !ok {
		return 0, nil
	}
	pool, err := lp.LeadPool(ctx)
	if errors.Is(err, errNoLeadPool) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if len(pool) == 0 {
		return 1, nil
	}
	var names []string
	for _, l := range pool {
		if !e164.MatchString(strings.TrimSpace(l.Phone)) {
			names = append(names, l.Name)
		}
	}
	if len(names) > 0 {
		slog.WarnContext(ctx, "escalation: ABT leads without a phone number on their CSM Portal profile",
			"leads", strings.Join(names, ", "))
	}
	return len(names), nil
}

// lowerTiersDialable reports whether any call on LEVEL_0..LEVEL_2 has a number.
func lowerTiersDialable(calls []PlannedCall) bool {
	for _, c := range calls {
		if c.Level <= Level2 && !c.HoldCall && strings.TrimSpace(c.Recipient.Phone) != "" {
			return true
		}
	}
	return false
}

// ladderChat picks the Chat client and room a ladder's rung cards go to.
//
// The room is chat.audience, then INCIDENT_ESCALATION_CHAT_AUDIENCE
// (fallbackRoom), then "Incident Monitor"; the file wins over the environment,
// as it does for the channel. With chat.webhookUrlEnv set, the ladder gets a
// client of its own holding just that one space, and shared -- built from
// GOOGLE_CHAT_SPACES -- is not used. Named but empty yields a client with no
// spaces, so every rung records NO_CHAT_SPACE instead of silently going to
// another room. getenv is a parameter so tests need not touch the process
// environment.
func ladderChat(c Chat, shared *notifications.GoogleChatClient, fallbackRoom string, getenv func(string) string) (*notifications.GoogleChatClient, string) {
	room := strings.TrimSpace(c.Audience)
	if room == "" {
		room = strings.TrimSpace(fallbackRoom)
	}
	if room == "" {
		room = chataudience.IncidentMonitor
	}
	name := strings.TrimSpace(c.WebhookURLEnv)
	if name == "" {
		return shared, room
	}
	url := strings.TrimSpace(getenv(name))
	if url == "" {
		slog.Error("escalation: chat.webhookUrlEnv names a variable that is not set; every rung will be recorded NO_CHAT_SPACE",
			"variable", name)
		return notifications.NewGoogleChatClient(notifications.GoogleChatConfig{}), room
	}
	return notifications.NewGoogleChatClient(notifications.GoogleChatConfig{
		AudienceSpaces: []notifications.GoogleChatAudienceSpace{{Audience: room, WebhookURL: url}},
	}), room
}
