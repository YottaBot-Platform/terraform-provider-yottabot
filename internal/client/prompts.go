package client

import "context"

const promptsPath = "/v1/agent-platform/prompts"

// Prompt mirrors a `prompts` row plus the body of its latest version.
//
// A prompt is a container; its content lives in versions. `Version` is the
// LATEST version's semver, computed on every read — not something stored on the
// prompt row itself.
type Prompt struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`

	// Version is the latest published version's semver.
	Version string `json:"version"`

	// Body and Variables come back only from GET /{id}, not from the list route
	// or the PATCH response. Together they are the latest version's content, and
	// reading a prompt without Variables is lossy: the declared input list is
	// part of the version's contract, so a client that imported one saw a
	// difference against its own config and published a spurious new version to
	// "restore" what was never missing.
	Body      string   `json:"body"`
	Variables []string `json:"variables"`

	// UsedBy counts the agents referencing this prompt. Computed.
	UsedBy int `json:"used_by"`

	CreatedByUserID *string `json:"created_by_user_id,omitempty"`
	CreatedAt       string  `json:"created_at"`
	ModifiedAt      string  `json:"modified_at"`
}

// PromptCreate is the POST body. The initial version is published alongside the
// prompt in one transaction.
type PromptCreate struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	InitialVersion string   `json:"initial_version"`
	Body           string   `json:"body"`
	Variables      []string `json:"variables"`
}

// PromptUpdate is the PATCH body. Nil preserves.
//
// Name and description are edited in place. A BODY IS NOT: prompt versions are
// immutable once published, because editing one would silently change what every
// agent already referencing that version executes. So Version + Body + Variables
// together PUBLISH A NEW VERSION, and the service refuses a body without a
// version rather than guessing a bump.
//
// The consequence for this provider: these three must be sent only when the
// content actually changed. Re-sending an unchanged version republishes it, and
// `UNIQUE (prompt_id, version)` turns that into a duplicate-key error on an
// update that only touched the description.
type PromptUpdate struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`

	Version   *string   `json:"version"`
	Body      *string   `json:"body"`
	Variables *[]string `json:"variables"`
}

func (c *Client) CreatePrompt(ctx context.Context, in PromptCreate) (*Prompt, error) {
	var out Prompt
	if err := c.Post(ctx, promptsPath, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetPrompt(ctx context.Context, id string) (*Prompt, error) {
	var out Prompt
	if err := c.Get(ctx, joinPath(promptsPath, id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdatePrompt(ctx context.Context, id string, in PromptUpdate) (*Prompt, error) {
	var out Prompt
	if err := c.Patch(ctx, joinPath(promptsPath, id), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeletePrompt removes the prompt and, by cascade, every version of it.
func (c *Client) DeletePrompt(ctx context.Context, id string) error {
	return c.Delete(ctx, joinPath(promptsPath, id))
}
