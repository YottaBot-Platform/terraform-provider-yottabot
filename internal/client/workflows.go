package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

const workflowsPath = "/v1/agent-platform/workflows"

// Workflow mirrors the workflow row the API returns.
type Workflow struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Status      string  `json:"status"`

	Trigger      string          `json:"trigger"`
	CronSchedule *string         `json:"cron_schedule"`
	Definition   json.RawMessage `json:"definition"`
	Tags         *string         `json:"tags"`

	OrchestratorID *string `json:"orchestrator_id"`

	LastRunAt     *string `json:"last_run_at"`
	LastRunStatus *string `json:"last_run_status"`
	CreatedAt     string  `json:"created_at"`
	ModifiedAt    string  `json:"modified_at"`
}

// WorkflowInput is the create/update body.
//
// The UPDATE statement splits the same three ways the agent route does:
//
//	description / cron_schedule / tags   COALESCE($n, col)      → "" clears
//	name / status / trigger              COALESCE(NULLIF($n,'')) → "" preserves
//	definition                           COALESCE($n::jsonb)     → null preserves
//
// definition is required by the Terraform schema, so it is always sent and the
// preserve case never arises.
//
// The server validates any definition carrying at least one step, so an
// unrunnable workflow is refused at apply with the full list of problems
// rather than persisting and failing at first run.
type WorkflowInput struct {
	Name        string  `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Status      string  `json:"status,omitempty"`

	Trigger      string          `json:"trigger,omitempty"`
	CronSchedule *string         `json:"cron_schedule,omitempty"`
	Definition   json.RawMessage `json:"definition,omitempty"`
	Tags         *string         `json:"tags,omitempty"`
}

func (c *Client) CreateWorkflow(ctx context.Context, in WorkflowInput) (*Workflow, error) {
	var out Workflow
	if err := c.Post(ctx, workflowsPath, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetWorkflow(ctx context.Context, id string) (*Workflow, error) {
	var out Workflow
	if err := c.Get(ctx, workflowPath(id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateWorkflow(ctx context.Context, id string, in WorkflowInput) (*Workflow, error) {
	var out Workflow
	if err := c.Patch(ctx, workflowPath(id), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteWorkflow(ctx context.Context, id string) error {
	return c.Delete(ctx, workflowPath(id))
}

func workflowPath(id string) string {
	return fmt.Sprintf("%s/%s", workflowsPath, url.PathEscape(id))
}
