package client

import "context"

const skillsPath = "/v1/sre-platform/skills"

// Skill mirrors an `sre_skills` row.
//
// ONE TABLE HOLDS TWO THINGS. A row with no account is a Yotta-managed catalog
// skill, shipped by seed and identical for every customer; a row with an account
// is that customer's own. There is no naming cue separating them — `AccountID`
// being nil IS the marker.
//
// Reads span both, deliberately: seeing the catalog is the point of having one.
// Writes reach only the caller's own rows, because every statement behind them
// carries the caller's account id, and nil never matches. So a managed skill can
// be listed and read here, and any attempt to change it answers 404.
type Skill struct {
	ID        string `json:"id"`
	AccountID *int64 `json:"account_id"`
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	Domain    string `json:"domain"`

	// Visibility is `yotta_managed`, `customer_visible` or `private`. A customer
	// skill may not claim the first — see SkillWrite.
	Visibility string `json:"visibility"`
	Status     string `json:"status"`

	// SourceKind is `yotta`, `imported` or `customer`. Server-set for anything
	// this client creates.
	SourceKind string `json:"source_kind"`

	SourceURL          *string `json:"source_url"`
	SourceCommit       *string `json:"source_commit"`
	License            *string `json:"license"`
	ImportReviewStatus string  `json:"import_review_status"`

	CreatedAt  string `json:"created_at"`
	ModifiedAt string `json:"modified_at"`
}

// SkillWrite is the create/update body for a CUSTOMER-owned skill.
//
// Note what is absent, and that each absence is a guard rather than an
// oversight:
//
//   - account_id — taken from the caller. Sending it would be a way to write a
//     row with no account, which is the Yotta-managed catalog marker.
//   - source_kind — pinned to `customer` server-side. `yotta` is the catalog's
//     own value; `imported` opens a provenance gate (pinned commit, license, an
//     approved review before it may go active), which is a governance workflow
//     rather than something a declarative config asserts about itself.
//   - the import-provenance fields — they belong to that same flow.
//
// `visibility` is narrower than the column's CHECK: a customer may set
// `customer_visible` or `private`, never `yotta_managed`, which would make the
// row read as Yotta-authored everywhere it is surfaced.
type SkillWrite struct {
	Slug       string  `json:"slug,omitempty"`
	Title      string  `json:"title,omitempty"`
	Domain     string  `json:"domain,omitempty"`
	Visibility *string `json:"visibility,omitempty"`
	Status     *string `json:"status,omitempty"`
}

func (c *Client) CreateSkill(ctx context.Context, in SkillWrite) (*Skill, error) {
	var out Skill
	if err := c.Post(ctx, skillsPath, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSkill reads one skill BY SLUG — that is the read route's shape, and it
// spans both ownership sets.
func (c *Client) GetSkill(ctx context.Context, slug string) (*Skill, error) {
	var out Skill
	if err := c.Get(ctx, joinPath(skillsPath, slug), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSkills returns the caller's own skills AND the Yotta-managed catalog.
// Used to resolve a slug to an id, since the write routes address by id.
func (c *Client) ListSkills(ctx context.Context) ([]Skill, error) {
	var out []Skill
	if err := c.Get(ctx, skillsPath, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateSkill and DeleteSkill address a skill BY ID under /id/{id}, not by slug,
// and the reason is not merely that GET already owns the single-segment space.
// A slug is NOT unique across the two ownership sets — the table carries two
// partial unique indexes precisely so a customer skill and a Yotta-managed one
// may share one — so a write addressed by slug would be ambiguous in exactly
// the case that matters.
func (c *Client) UpdateSkill(ctx context.Context, id string, in SkillWrite) (*Skill, error) {
	var out Skill
	if err := c.Patch(ctx, joinPath(skillsPath+"/id", id), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteSkill(ctx context.Context, id string) error {
	return c.Delete(ctx, joinPath(skillsPath+"/id", id))
}
