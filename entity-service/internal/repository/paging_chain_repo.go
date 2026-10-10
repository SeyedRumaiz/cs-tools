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

package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

// PagingChainRepository reads and changes the people on a Case Paging chain:
// the team_member rows behind each tier. See domain.PagingChainMember.
//
// Every write names the actor for the audit trigger (migration 0214) and runs
// in one transaction, so a move ("make X the 2nd responder", which takes the
// slot off whoever held it) is never half applied.
type PagingChainRepository interface {
	// ListMembers is every membership of the rostered teams in family (CRE or
	// SRE), the CRE leadership team included under CRE.
	ListMembers(ctx context.Context, family string) ([]domain.PagingChainMember, error)
	// GetMember is one membership, with whether its team has an America lead;
	// a NotFoundError when there is none.
	GetMember(ctx context.Context, membershipID string) (PagingChainTarget, error)
	// CallerAuthority is what the caller holds: their global role names (an
	// internal user's only) and their team memberships.
	CallerAuthority(ctx context.Context, email string) (PagingCaller, error)

	SetResponderRank(ctx context.Context, actorEmail, membershipID string, rank int) error
	SetRole(ctx context.Context, actorEmail, membershipID, role string) error

	// ReadinessFacts is what the paging readiness check is computed from,
	// for the rota dates from..to (YYYY-MM-DD, inclusive). See
	// PagingReadinessFacts.
	ReadinessFacts(ctx context.Context, from, to string) (PagingReadinessFacts, error)
}

// PagingChainTarget is a membership about to be changed, with what its team
// looks like around it.
type PagingChainTarget struct {
	domain.PagingChainMember
	// TeamHasHead is whether the team has an America lead
	// (americas_team_lead) other than this member.
	TeamHasHead bool
}

// PagingCaller is what decides which edits a caller may make.
type PagingCaller struct {
	GlobalRoles []string
	Memberships []PagingCallerMembership
}

// PagingCallerMembership is one of the caller's own memberships.
type PagingCallerMembership struct {
	TeamKey string
	Role    string
}

type pagingChainRepository struct{ db *pgxpool.Pool }

// NewPagingChainRepository constructs a PagingChainRepository over the pool.
func NewPagingChainRepository(db *pgxpool.Pool) PagingChainRepository {
	return &pagingChainRepository{db: db}
}

// pagingTeamWhere is the teams a paging chain is made of: every SRE team, the
// CRE ABTs, the Americas team and the CRE leadership team. rosteredTeamWhere
// leaves the leadership team out, because nobody on it works a rota, but it is
// where the CRE head and CS head sit, and they are the chain's top tier.
//
// Other teams of type cre (Migration, Australia, onboarding) are left out: they
// are not on the paging rota, so no rung ever reaches them, and listing them
// offered responder and lead pickers that page nobody. Americas is recognised
// by name or key, as migrations 0202 and 0203 do. SME has no chain of its own
// here: its page follows the rota.
const pagingTeamWhere = `t.type IS NOT NULL AND (lower(t.type) LIKE 'sre%'` +
	` OR lower(t.type) IN ('cre-abt', 'cre-leadership')` +
	` OR (lower(t.type) = 'cre' AND (lower(coalesce(t.key, '')) ~ '` + americasTeamPattern + `'` +
	` OR lower(t.name) ~ '` + americasTeamPattern + `')))`

// responderRankExpr turns the stored alert_tier into the rank the tab shows.
const responderRankExpr = `CASE m.alert_tier WHEN 'T1' THEN 1 WHEN 'T2' THEN 2 WHEN 'T3' THEN 3 ELSE 0 END`

// pagingMemberColumns is one PagingChainMember. engineerName, teamFamilyExpr
// and teamDisplayOrder are the schedule repository's own, so this tab and the
// rota agree on names and families.
const pagingMemberColumns = `
       m.id::text, t.key, t.name, COALESCE(t.type, ''), ` + teamFamilyExpr + `,
       u.id::text, ` + engineerName + `, COALESCE(u.email, ''),
       m.role, ` + responderRankExpr

func scanPagingMember(row pgx.Row, m *domain.PagingChainMember, extra ...any) error {
	dest := []any{&m.MembershipID, &m.TeamKey, &m.TeamName, &m.TeamType, &m.Family,
		&m.UserID, &m.Name, &m.Email, &m.Role, &m.ResponderRank}
	return row.Scan(append(dest, extra...)...)
}

func (r *pagingChainRepository) ListMembers(ctx context.Context, family string) ([]domain.PagingChainMember, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+pagingMemberColumns+`
		  FROM team_member m
		  JOIN team t    ON t.id = m.team_id
		  JOIN "user" u  ON u.id = m.user_id
		 WHERE `+pagingTeamWhere+`
		   AND `+teamFamilyExpr+` = $1
		 ORDER BY `+teamDisplayOrder+`, m.role, `+responderRankExpr+`, `+engineerName,
		family)
	if err != nil {
		return nil, fmt.Errorf("query paging chain members: %w", err)
	}
	defer rows.Close()
	out := []domain.PagingChainMember{}
	for rows.Next() {
		var m domain.PagingChainMember
		if err := scanPagingMember(rows, &m); err != nil {
			return nil, fmt.Errorf("scan paging chain member: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *pagingChainRepository) GetMember(ctx context.Context, membershipID string) (PagingChainTarget, error) {
	var t PagingChainTarget
	err := scanPagingMember(r.db.QueryRow(ctx, `
		SELECT `+pagingMemberColumns+`,
		       EXISTS (SELECT 1 FROM team_member h
		                WHERE h.team_id = m.team_id AND h.role = 'americas_team_lead' AND h.id <> m.id)
		  FROM team_member m
		  JOIN team t    ON t.id = m.team_id
		  JOIN "user" u  ON u.id = m.user_id
		 WHERE m.id = $1::uuid`, membershipID), &t.PagingChainMember, &t.TeamHasHead)
	if errors.Is(err, pgx.ErrNoRows) {
		return PagingChainTarget{}, &apierror.NotFoundError{Msg: "team membership not found"}
	}
	if err != nil {
		return PagingChainTarget{}, fmt.Errorf("get paging chain member: %w", err)
	}
	return t, nil
}

func (r *pagingChainRepository) CallerAuthority(ctx context.Context, email string) (PagingCaller, error) {
	var c PagingCaller
	// Global roles only for an internal user, the same rule RotaAdminTeamsFor
	// applies: a rota admin or admin role on a customer account grants nothing.
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT ro.name
		  FROM "user" u
		  JOIN user_role ur ON ur.user_id = u.id
		  JOIN role ro      ON ro.id = ur.role_id
		 WHERE lower(u.email) = lower($1)
		   AND u.user_type = 'INTERNAL'::user_type_enum`, email)
	if err != nil {
		return c, fmt.Errorf("query caller roles: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return c, fmt.Errorf("scan caller role: %w", err)
		}
		c.GlobalRoles = append(c.GlobalRoles, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, fmt.Errorf("iterate caller roles: %w", err)
	}

	rows, err = r.db.Query(ctx, `
		SELECT t.key, m.role
		  FROM team_member m
		  JOIN team t   ON t.id = m.team_id
		  JOIN "user" u ON u.id = m.user_id
		 WHERE lower(u.email) = lower($1)
		   AND t.key IS NOT NULL`, email)
	if err != nil {
		return c, fmt.Errorf("query caller memberships: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m PagingCallerMembership
		if err := rows.Scan(&m.TeamKey, &m.Role); err != nil {
			return c, fmt.Errorf("scan caller membership: %w", err)
		}
		c.Memberships = append(c.Memberships, m)
	}
	return c, rows.Err()
}

// inTx runs fn in one transaction with the actor named for the audit trigger.
func (r *pagingChainRepository) inTx(ctx context.Context, actorEmail string, fn func(pgx.Tx) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin paging chain change: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := nameTheActor(ctx, tx, actorEmail); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return mapPagingChainError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit paging chain change: %w", err)
	}
	return nil
}

// mustTouch turns "no row matched the id" into a NotFoundError.
func mustTouch(tag pgconn.CommandTag, err error) error {
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return &apierror.NotFoundError{Msg: "team membership not found"}
	}
	return nil
}

func (r *pagingChainRepository) SetResponderRank(ctx context.Context, actorEmail, membershipID string, rank int) error {
	var slot *string
	if rank > 0 {
		s := fmt.Sprintf("T%d", rank)
		slot = &s
	}
	return r.inTx(ctx, actorEmail, func(tx pgx.Tx) error {
		if slot != nil {
			// Take the slot off whoever holds it on this team first: one
			// person per slot per team is a unique index (migration 0171).
			if _, err := tx.Exec(ctx, `
				UPDATE team_member
				   SET alert_tier = NULL, updated_on = now(), updated_by = $3
				 WHERE team_id = (SELECT team_id FROM team_member WHERE id = $1::uuid)
				   AND alert_tier = $2 AND id <> $1::uuid`, membershipID, *slot, actorEmail); err != nil {
				return fmt.Errorf("free responder slot: %w", err)
			}
		}
		return mustTouch(tx.Exec(ctx, `
			UPDATE team_member
			   SET alert_tier = $2, updated_on = now(), updated_by = $3
			 WHERE id = $1::uuid`, membershipID, slot, actorEmail))
	})
}

func (r *pagingChainRepository) SetRole(ctx context.Context, actorEmail, membershipID, role string) error {
	return r.inTx(ctx, actorEmail, func(tx pgx.Tx) error {
		switch role {
		case "cre_head", "cs_head":
			// One CRE head and one CS head: picking a new one steps the
			// previous holder back to engineer.
			if _, err := tx.Exec(ctx, `
				UPDATE team_member
				   SET role = 'engineer', updated_on = now(), updated_by = $3
				 WHERE role = $2 AND id <> $1::uuid`, membershipID, role, actorEmail); err != nil {
				return fmt.Errorf("step down previous %s: %w", role, err)
			}
		case "americas_team_lead":
			// One America lead per team (index team_member_one_americas_team_lead):
			// the previous one steps back to one of the team's Team leads.
			if _, err := tx.Exec(ctx, `
				UPDATE team_member
				   SET role = 'lead', updated_on = now(), updated_by = $2
				 WHERE team_id = (SELECT team_id FROM team_member WHERE id = $1::uuid)
				   AND role = 'americas_team_lead' AND id <> $1::uuid`, membershipID, actorEmail); err != nil {
				return fmt.Errorf("step down previous America lead: %w", err)
			}
		}
		// A lead or head holds no responder slot (constraint
		// team_member_alert_tier_not_lead), so a promotion clears it.
		return mustTouch(tx.Exec(ctx, `
			UPDATE team_member
			   SET role = $2,
			       alert_tier = CASE WHEN $2 IN ('lead', 'americas_team_lead', 'cre_head', 'cs_head') THEN NULL ELSE alert_tier END,
			       updated_on = now(), updated_by = $3
			 WHERE id = $1::uuid`, membershipID, role, actorEmail))
	})
}

// mapPagingChainError turns the constraints the service already checks into
// a 400 or 409 naming the rule, in case a race slips past the check. It never
// returns the constraint's own detail, which quotes table internals.
func mapPagingChainError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.ConstraintName {
	case "team_member_alert_tier_not_lead":
		return &apierror.ValidationError{Msg: "a Team lead or head cannot also be a responder"}
	case "team_member_alert_tier_unique", "team_member_one_americas_team_lead":
		return &apierror.ConflictError{Msg: "someone else changed this team at the same time; reload and try again"}
	}
	return err
}

// PagingReadinessFacts is everything the readiness check reads, gathered in a
// fixed handful of queries whatever the date range, so the check itself is a
// pure function over it (service.computePagingReadiness).
type PagingReadinessFacts struct {
	// CRETeams are the CRE ABTs (type cre-abt) and the Americas team.
	CRETeams []PagingReadinessTeam
	// CREMembers are the memberships of CRETeams, and every cre_head and
	// cs_head wherever they sit.
	CREMembers []PagingReadinessMember
	// Rotas are the active rotas, every family.
	Rotas []PagingReadinessRota
	// Windows are the active escalation windows of the active zones a rota
	// owns. Regular hours are not paged, so they are not here.
	Windows []PagingReadinessWindow
	// SMETeams are the teams whose type is SME's, with the rota their type
	// puts them on (empty for none).
	SMETeams []PagingReadinessTeam
	// Assignments are the shifts on those windows in the range, less the ones
	// whose engineer is away that day -- the same rule the on-duty read the
	// ladders page from applies.
	Assignments []PagingReadinessAssignment
	// AccountCRETeams are the CRE team names live accounts carry, lower-cased
	// and trimmed, with how many accounts carry each.
	AccountCRETeams []PagingReadinessAccountTeam
	// CREAbsences are the absences of the people in CREMembers that overlap
	// the range, so a position held by someone away can be reported.
	CREAbsences []PagingReadinessAbsence
	// PagingPhones is every stored paging-only number's last test status, by
	// user id ("" when never tested). The numbers themselves are not read.
	PagingPhones map[string]string
	// ProfilePhones is the team members who have a callable number on their
	// own profile ("user".phone), by user id. The numbers are not read.
	ProfilePhones map[string]bool
}

// PagingReadinessAbsence is one absence: the dates it covers (EndsOn empty
// when open-ended), its label, and the team it moves the person to, if any
// -- someone moved to a team is away from every team but that one.
type PagingReadinessAbsence struct {
	UserID      string
	StartsOn    string
	EndsOn      string
	Label       string
	MovesToTeam string
}

// PagingReadinessTeam is one team as the readiness check needs it.
type PagingReadinessTeam struct {
	Key        string
	Name       string
	Type       string // lower-cased team.type
	IsAmericas bool
	RotaCode   string
}

// PagingReadinessMember is one membership: the team's key (empty for a head
// whose team has none), the role and the alert tier (T1-T3, or empty).
type PagingReadinessMember struct {
	UserID    string
	TeamKey   string
	Role      string
	AlertTier string
	Name      string
	Email     string
}

// PagingReadinessRota is one active rota.
type PagingReadinessRota struct {
	Code   string
	Label  string
	Family string
}

// PagingReadinessWindow is one escalation window. Tier is the window's fixed
// tier, empty where the assignment carries it.
type PagingReadinessWindow struct {
	ShiftCode     string
	ShiftLabel    string
	ZoneCode      string
	ZoneLabel     string
	ZoneSortOrder int
	RotaCode      string
	DayScope      string
	Tier          string
}

// PagingReadinessAssignment is one rostered shift. Tier is the assignment's,
// else the window's; empty when neither has one.
type PagingReadinessAssignment struct {
	UserID    string
	RotaDate  string
	ShiftCode string
	ZoneCode  string
	RotaCode  string
	TeamKey   string
	Tier      string
	Name      string
	Email     string
}

// PagingReadinessAccountTeam is one CRE team name accounts carry.
type PagingReadinessAccountTeam struct {
	Name     string
	Accounts int
}

// americasTeamPattern is how migrations 0202 and 0203 recognise the Americas
// team: by name or key, because the key differs between environments.
const americasTeamPattern = `^americas([ _-]+cre)?([ _-]+team)?$`

func (r *pagingChainRepository) ReadinessFacts(ctx context.Context, from, to string) (PagingReadinessFacts, error) {
	var f PagingReadinessFacts

	creTeamWhere := `t.key IS NOT NULL AND (lower(COALESCE(t.type, '')) = 'cre-abt'
		OR lower(COALESCE(t.name, '')) ~ $1 OR lower(t.key) ~ $1)`
	if err := collectPagingRows(ctx, r.db, &f.CRETeams, func(row pgx.Rows, t *PagingReadinessTeam) error {
		return row.Scan(&t.Key, &t.Name, &t.Type, &t.IsAmericas)
	}, `
		SELECT t.key, COALESCE(t.name, ''), lower(COALESCE(t.type, '')),
		       (lower(COALESCE(t.name, '')) ~ $1 OR lower(t.key) ~ $1)
		  FROM team t
		 WHERE `+creTeamWhere+`
		 ORDER BY t.key`, americasTeamPattern); err != nil {
		return f, fmt.Errorf("query paging readiness CRE teams: %w", err)
	}

	if err := collectPagingRows(ctx, r.db, &f.CREMembers, func(row pgx.Rows, m *PagingReadinessMember) error {
		return row.Scan(&m.UserID, &m.TeamKey, &m.Role, &m.AlertTier, &m.Name, &m.Email)
	}, `
		SELECT m.user_id::text, COALESCE(t.key, ''), m.role, COALESCE(m.alert_tier, ''), `+engineerName+`, COALESCE(u.email, '')
		  FROM team_member m
		  JOIN team t   ON t.id = m.team_id
		  JOIN "user" u ON u.id = m.user_id
		 WHERE m.role IN ('cre_head', 'cs_head') OR (`+creTeamWhere+`)
		 ORDER BY 2, 5`, americasTeamPattern); err != nil {
		return f, fmt.Errorf("query paging readiness CRE members: %w", err)
	}

	if err := collectPagingRows(ctx, r.db, &f.Rotas, func(row pgx.Rows, ro *PagingReadinessRota) error {
		return row.Scan(&ro.Code, &ro.Label, &ro.Family)
	}, `
		SELECT code, label, family::text FROM team_schedule_rota
		 WHERE is_active ORDER BY family, sort_order, code`); err != nil {
		return f, fmt.Errorf("query paging readiness rotas: %w", err)
	}

	if err := collectPagingRows(ctx, r.db, &f.Windows, func(row pgx.Rows, w *PagingReadinessWindow) error {
		return row.Scan(&w.ShiftCode, &w.ShiftLabel, &w.ZoneCode, &w.ZoneLabel, &w.ZoneSortOrder, &w.RotaCode, &w.DayScope, &w.Tier)
	}, `
		SELECT s.code, s.label, z.code, z.label, z.sort_order::int, ro.code, s.day_scope::text, COALESCE(s.tier::text, '')
		  FROM team_schedule_shift s
		  JOIN team_schedule_zone z  ON z.id = s.zone_id AND z.is_active
		  JOIN team_schedule_rota ro ON ro.id = z.rota_id AND ro.is_active
		 WHERE s.is_active AND s.is_escalation
		 ORDER BY ro.code, z.sort_order, s.sort_order`); err != nil {
		return f, fmt.Errorf("query paging readiness windows: %w", err)
	}

	if err := collectPagingRows(ctx, r.db, &f.SMETeams, func(row pgx.Rows, t *PagingReadinessTeam) error {
		return row.Scan(&t.Key, &t.Name, &t.Type, &t.RotaCode)
	}, `
		SELECT t.key, COALESCE(t.name, ''), lower(t.type), COALESCE(ro.code, '')
		  FROM team t
		  LEFT JOIN team_schedule_rota ro ON lower(ro.team_type) = lower(t.type) AND ro.is_active
		 WHERE t.key IS NOT NULL AND lower(COALESCE(t.type, '')) LIKE 'sme%'
		 ORDER BY t.key`); err != nil {
		return f, fmt.Errorf("query paging readiness SME teams: %w", err)
	}

	// Somebody away that day is not paged, whatever their assignment says:
	// the same exclusion OnDutyAt makes, by the rota date rather than by an
	// instant, which is what a readiness check over whole days can ask.
	if err := collectPagingRows(ctx, r.db, &f.Assignments, func(row pgx.Rows, a *PagingReadinessAssignment) error {
		var d time.Time
		if err := row.Scan(&a.UserID, &d, &a.ShiftCode, &a.ZoneCode, &a.RotaCode, &a.TeamKey, &a.Tier, &a.Name, &a.Email); err != nil {
			return err
		}
		a.RotaDate = d.Format("2006-01-02")
		return nil
	}, `
		SELECT a.user_id::text, a.rota_date, s.code, z.code, ro.code, a.team_key, COALESCE(a.tier::text, s.tier::text, ''),
		       `+engineerName+`, COALESCE(u.email, '')
		  FROM team_schedule_assignment a
		  JOIN "user" u               ON u.id = a.user_id
		  JOIN team_schedule_shift s  ON s.id = a.shift_id AND s.is_escalation
		  JOIN team_schedule_zone z   ON z.id = s.zone_id
		  JOIN team_schedule_rota ro  ON ro.id = z.rota_id
		 WHERE a.rota_date BETWEEN $1::date AND $2::date
		   AND NOT EXISTS (
		        SELECT 1 FROM team_schedule_absence ab
		        JOIN team_schedule_absence_kind k ON k.id = ab.kind_id
		        WHERE ab.user_id = a.user_id
		          AND daterange(ab.starts_on, ab.ends_on, '[]') @> a.rota_date
		          AND NOT (k.moves_to_team_key IS NOT NULL
		                   AND lower(k.moves_to_team_key) = lower(a.team_key)))
		 ORDER BY a.rota_date, ro.code, z.code, 8`, from, to); err != nil {
		return f, fmt.Errorf("query paging readiness assignments: %w", err)
	}

	if err := collectPagingRows(ctx, r.db, &f.AccountCRETeams, func(row pgx.Rows, t *PagingReadinessAccountTeam) error {
		return row.Scan(&t.Name, &t.Accounts)
	}, `
		SELECT lower(trim(g.name)), count(*)::int
		  FROM account a
		  JOIN "group" g ON g.id = a.cre_team_id
		 WHERE a.deleted_on IS NULL AND trim(COALESCE(g.name, '')) <> ''
		 GROUP BY 1 ORDER BY 1`); err != nil {
		return f, fmt.Errorf("query paging readiness account teams: %w", err)
	}

	// Absences of anyone holding a CRE position, where they meet the range.
	if err := collectPagingRows(ctx, r.db, &f.CREAbsences, func(row pgx.Rows, a *PagingReadinessAbsence) error {
		var starts time.Time
		var ends *time.Time
		if err := row.Scan(&a.UserID, &starts, &ends, &a.Label, &a.MovesToTeam); err != nil {
			return err
		}
		a.StartsOn = starts.Format(time.DateOnly)
		if ends != nil {
			a.EndsOn = ends.Format(time.DateOnly)
		}
		return nil
	}, `
		SELECT ab.user_id::text, ab.starts_on, ab.ends_on, k.label, COALESCE(k.moves_to_team_key, '')
		  FROM team_schedule_absence ab
		  JOIN team_schedule_absence_kind k ON k.id = ab.kind_id
		 WHERE daterange(ab.starts_on, ab.ends_on, '[]') && daterange($2::date, $3::date, '[]')
		   AND ab.user_id IN (
		        SELECT m.user_id FROM team_member m JOIN team t ON t.id = m.team_id
		         WHERE m.role IN ('cre_head', 'cs_head') OR (`+creTeamWhere+`))
		 ORDER BY 1, 2`, americasTeamPattern, from, to); err != nil {
		return f, fmt.Errorf("query paging readiness absences: %w", err)
	}

	f.PagingPhones = map[string]string{}
	rows, err := r.db.Query(ctx, `SELECT user_id::text, COALESCE(last_test_status, '') FROM paging_contact`)
	if err != nil {
		return f, fmt.Errorf("query paging readiness phones: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			return f, fmt.Errorf("scan paging readiness phone: %w", err)
		}
		f.PagingPhones[id] = status
	}
	if err := rows.Err(); err != nil {
		return f, fmt.Errorf("read paging readiness phones: %w", err)
	}

	f.ProfilePhones = map[string]bool{}
	prow, err := r.db.Query(ctx, `
		SELECT u.id::text FROM "user" u
		 WHERE btrim(u.phone) ~ $1
		   AND EXISTS (SELECT 1 FROM team_member m WHERE m.user_id = u.id)`, CallablePhonePattern)
	if err != nil {
		return f, fmt.Errorf("query paging readiness profile phones: %w", err)
	}
	defer prow.Close()
	for prow.Next() {
		var id string
		if err := prow.Scan(&id); err != nil {
			return f, fmt.Errorf("scan paging readiness profile phone: %w", err)
		}
		f.ProfilePhones[id] = true
	}
	return f, prow.Err()
}

// collectPagingRows runs query and appends one scanned T per row to out, which is
// never left nil.
func collectPagingRows[T any](ctx context.Context, db *pgxpool.Pool, out *[]T, scan func(pgx.Rows, *T) error, query string, args ...any) error {
	*out = []T{}
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var v T
		if err := scan(rows, &v); err != nil {
			return err
		}
		*out = append(*out, v)
	}
	return rows.Err()
}
