package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	dbgen "github.com/thkx/agentmesh/internal/db"
	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	pland "github.com/thkx/agentmesh/internal/domain/planning"
)

type GoalRepository struct{ q *dbgen.Queries }
type PlanRepository struct{ q *dbgen.Queries }

func NewGoalRepository(sqlDB *sql.DB) (*GoalRepository, error) {
	if sqlDB == nil {
		return nil, errors.New("goal repository requires a database")
	}
	return &GoalRepository{q: dbgen.New(sqlDB)}, nil
}

func NewPlanRepository(sqlDB *sql.DB) (*PlanRepository, error) {
	if sqlDB == nil {
		return nil, errors.New("plan repository requires a database")
	}
	return &PlanRepository{q: dbgen.New(sqlDB)}, nil
}

func (r *GoalRepository) Create(ctx context.Context, g goaldom.Goal) error {
	constraints, err := json.Marshal(g.Constraints)
	if err != nil {
		return fmt.Errorf("marshal goal constraints: %w", err)
	}
	criteria, err := json.Marshal(g.SuccessCriteria)
	if err != nil {
		return fmt.Errorf("marshal success criteria: %w", err)
	}
	contextJSON, err := json.Marshal(g.Context)
	if err != nil {
		return fmt.Errorf("marshal goal context: %w", err)
	}
	return r.q.CreateAgentGoal(ctx, dbgen.CreateAgentGoalParams{
		ID: g.ID, Description: g.Description, ConstraintsJson: string(constraints), SuccessCriteriaJson: string(criteria), ContextJson: string(contextJSON),
		Status: string(g.Status), CreatedAt: g.CreatedAt.UnixMilli(), UpdatedAt: g.UpdatedAt.UnixMilli(), CanceledAt: 0,
	})
}

func (r *GoalRepository) Update(ctx context.Context, g goaldom.Goal) error {
	constraints, err := json.Marshal(g.Constraints)
	if err != nil {
		return fmt.Errorf("marshal goal constraints: %w", err)
	}
	criteria, err := json.Marshal(g.SuccessCriteria)
	if err != nil {
		return fmt.Errorf("marshal success criteria: %w", err)
	}
	contextJSON, err := json.Marshal(g.Context)
	if err != nil {
		return fmt.Errorf("marshal goal context: %w", err)
	}
	affected, err := r.q.UpdateAgentGoal(ctx, dbgen.UpdateAgentGoalParams{Description: g.Description, ConstraintsJson: string(constraints), SuccessCriteriaJson: string(criteria), ContextJson: string(contextJSON), UpdatedAt: g.UpdatedAt.UnixMilli(), ID: g.ID})
	if err != nil {
		return fmt.Errorf("update goal: %w", err)
	}
	if affected == 0 {
		_, getErr := r.Get(ctx, g.ID)
		if getErr != nil {
			return getErr
		}
		return goaldom.ErrInvalidTransition
	}
	return nil
}

func (r *GoalRepository) Get(ctx context.Context, id string) (goaldom.Goal, error) {
	row, err := r.q.GetAgentGoal(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return goaldom.Goal{}, goaldom.ErrNotFound
	}
	if err != nil {
		return goaldom.Goal{}, fmt.Errorf("get goal: %w", err)
	}
	return decodeGoal(row)
}

func (r *GoalRepository) List(ctx context.Context, limit int) ([]goaldom.Goal, error) {
	rows, err := r.q.ListAgentGoals(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("list goals: %w", err)
	}
	out := make([]goaldom.Goal, 0, len(rows))
	for _, row := range rows {
		g, decodeErr := decodeGoal(row)
		if decodeErr != nil {
			return nil, decodeErr
		}
		out = append(out, g)
	}
	return out, nil
}

func (r *GoalRepository) Cancel(ctx context.Context, id string, at time.Time) error {
	affected, err := r.q.CancelAgentGoal(ctx, dbgen.CancelAgentGoalParams{UpdatedAt: at.UnixMilli(), CanceledAt: at.UnixMilli(), ID: id})
	if err != nil {
		return fmt.Errorf("cancel goal: %w", err)
	}
	if affected == 0 {
		_, getErr := r.Get(ctx, id)
		if getErr != nil {
			return getErr
		}
		return goaldom.ErrInvalidTransition
	}
	return nil
}

func decodeGoal(row dbgen.AgentGoal) (goaldom.Goal, error) {
	var g goaldom.Goal
	if err := json.Unmarshal([]byte(row.ConstraintsJson), &g.Constraints); err != nil {
		return g, fmt.Errorf("decode goal constraints: %w", err)
	}
	if err := json.Unmarshal([]byte(row.SuccessCriteriaJson), &g.SuccessCriteria); err != nil {
		return g, fmt.Errorf("decode goal success criteria: %w", err)
	}
	if err := json.Unmarshal([]byte(row.ContextJson), &g.Context); err != nil {
		return g, fmt.Errorf("decode goal context: %w", err)
	}
	if g.Constraints == nil {
		g.Constraints = []string{}
	}
	if g.SuccessCriteria == nil {
		g.SuccessCriteria = []goaldom.SuccessCriterion{}
	}
	g.ID, g.Description, g.Status = row.ID, row.Description, goaldom.Status(row.Status)
	if row.CompletedAt > 0 {
		completed := time.UnixMilli(row.CompletedAt).UTC()
		g.CompletedAt = &completed
		g.Status = goaldom.StatusCompleted
	}
	g.CreatedAt, g.UpdatedAt = time.UnixMilli(row.CreatedAt).UTC(), time.UnixMilli(row.UpdatedAt).UTC()
	if row.CanceledAt > 0 {
		canceled := time.UnixMilli(row.CanceledAt).UTC()
		g.CanceledAt = &canceled
	}
	return g, nil
}

func (r *PlanRepository) Create(ctx context.Context, p pland.Plan) (pland.Plan, error) {
	steps, err := json.Marshal(p.Steps)
	if err != nil {
		return pland.Plan{}, fmt.Errorf("marshal plan steps: %w", err)
	}
	version, err := r.q.CreateAgentPlan(ctx, dbgen.CreateAgentPlanParams{
		ID: p.ID, GoalID: p.GoalID, Objective: p.Objective, StepsJson: string(steps), Status: string(p.Status), ModelID: p.ModelID, CreatedAt: p.CreatedAt.UnixMilli(),
	})
	if err != nil {
		return pland.Plan{}, fmt.Errorf("persist plan: %w", err)
	}
	p.Version = int(version)
	return p, nil
}

func (r *PlanRepository) Get(ctx context.Context, goalID string, version int) (pland.Plan, error) {
	var row dbgen.AgentPlan
	var err error
	if version <= 0 {
		row, err = r.q.GetLatestAgentPlan(ctx, goalID)
	} else {
		row, err = r.q.GetAgentPlan(ctx, dbgen.GetAgentPlanParams{GoalID: goalID, Version: int64(version)})
	}
	if errors.Is(err, sql.ErrNoRows) {
		return pland.Plan{}, fmt.Errorf("%w: plan for goal %s", goaldom.ErrNotFound, goalID)
	}
	if err != nil {
		return pland.Plan{}, fmt.Errorf("get plan: %w", err)
	}
	return decodePlan(row)
}

func (r *PlanRepository) List(ctx context.Context, goalID string, limit int) ([]pland.Plan, error) {
	rows, err := r.q.ListAgentPlans(ctx, dbgen.ListAgentPlansParams{GoalID: goalID, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	out := make([]pland.Plan, 0, len(rows))
	for _, row := range rows {
		p, decodeErr := decodePlan(row)
		if decodeErr != nil {
			return nil, decodeErr
		}
		out = append(out, p)
	}
	return out, nil
}

func decodePlan(row dbgen.AgentPlan) (pland.Plan, error) {
	var steps []pland.Step
	if err := json.Unmarshal([]byte(row.StepsJson), &steps); err != nil {
		return pland.Plan{}, fmt.Errorf("decode plan steps: %w", err)
	}
	if steps == nil {
		steps = []pland.Step{}
	}
	return pland.Plan{ID: row.ID, GoalID: row.GoalID, Version: int(row.Version), Objective: row.Objective, Steps: steps, Status: pland.PlanStatus(row.Status), ModelID: row.ModelID, CreatedAt: time.UnixMilli(row.CreatedAt).UTC()}, nil
}

var _ interface {
	Create(context.Context, goaldom.Goal) error
	Update(context.Context, goaldom.Goal) error
	Get(context.Context, string) (goaldom.Goal, error)
	List(context.Context, int) ([]goaldom.Goal, error)
	Cancel(context.Context, string, time.Time) error
} = (*GoalRepository)(nil)
var _ interface {
	Create(context.Context, pland.Plan) (pland.Plan, error)
	Get(context.Context, string, int) (pland.Plan, error)
	List(context.Context, string, int) ([]pland.Plan, error)
} = (*PlanRepository)(nil)
