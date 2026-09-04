package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Acceptance tests drive a real `terraform apply` against a real YottaBot
// estate. They create and destroy real rows, so they run only when TF_ACC is
// set — resource.Test skips otherwise.
//
// NEVER point these at production. Use a disposable estate.
//
// Required:
//
//	TF_ACC=1
//	YOTTABOT_ENDPOINT   the estate base URL
//	YOTTABOT_TOKEN      a bearer token, or the service-account triple
//	                    (YOTTABOT_USER_ID / YOTTABOT_KID / YOTTABOT_PRIVATE_KEY_PEM)
//
// The service account needs agents, workflows, context_parent_handles,
// mcp_gateways and tools read/write/delete, plus users:write — creating an
// agent mints its linked user, so an otherwise-correct policy still 403s.

var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"yottabot": providerserver.NewProtocol6WithError(New("acctest")()),
}

func testAccPreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("YOTTABOT_ENDPOINT") == "" {
		t.Fatal("YOTTABOT_ENDPOINT must be set for acceptance tests")
	}
	if os.Getenv("YOTTABOT_TOKEN") == "" && os.Getenv("YOTTABOT_USER_ID") == "" {
		t.Fatal("set YOTTABOT_TOKEN, or the YOTTABOT_USER_ID/KID/PRIVATE_KEY_PEM triple")
	}
}

// accName gives each run its own names. Two things make this load-bearing
// rather than tidy: a failed run leaves rows behind, and a Context provider
// survives destroy as a retired row still holding its unique key — so reusing
// an external_id makes the NEXT run fail on a constraint rather than on
// anything it did wrong.
func accName(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("tfacc-%s-%s", prefix, os.Getenv("TF_ACC_RUN_ID"))
}

func TestAccAgent_lifecycle(t *testing.T) {
	name := accName(t, "agent")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "yottabot_agent" "test" {
  name        = %q
  description = "created by the provider acceptance suite"
  status      = "draft"
}
`, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("yottabot_agent.test", "name", name),
					resource.TestCheckResourceAttr("yottabot_agent.test", "status", "draft"),
					resource.TestCheckResourceAttrSet("yottabot_agent.test", "id"),
					// Creating an agent mints its linked user; the id coming
					// back is what proves users:write was actually granted.
					resource.TestCheckResourceAttrSet("yottabot_agent.test", "user_id"),
				),
			},
			{
				ResourceName:      "yottabot_agent.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Update in place. description is one of the fields whose
				// empty value means "preserve" on the wire, so this also
				// proves a non-empty update actually lands.
				Config: fmt.Sprintf(`
resource "yottabot_agent" "test" {
  name        = %q
  description = "updated by the provider acceptance suite"
  status      = "draft"
}
`, name),
				Check: resource.TestCheckResourceAttr(
					"yottabot_agent.test", "description", "updated by the provider acceptance suite"),
			},
		},
	})
}

func TestAccWorkflow_lifecycle(t *testing.T) {
	name := accName(t, "workflow")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "yottabot_workflow" "test" {
  name    = %q
  status  = "draft"
  trigger = "manual"

  definition_json = jsonencode({
    steps = [{
      name   = "noop"
      type   = "agent_call"
      output = "result"
    }]
  })
}
`, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("yottabot_workflow.test", "name", name),
					resource.TestCheckResourceAttrSet("yottabot_workflow.test", "id"),
				),
			},
			{
				ResourceName:      "yottabot_workflow.test",
				ImportState:       true,
				ImportStateVerify: true,
				// ImportStateVerify compares state attributes as raw strings,
				// which bypasses the semantic JSON equality definition_json
				// uses — the imported value is the server's key order, and the
				// configured value is ours. They are equal as JSON and unequal
				// as strings. The PlanOnly step below is what actually proves
				// the semantic comparison works; verifying the string here
				// would only assert that the server preserves our key order,
				// which it does not promise.
				ImportStateVerifyIgnore: []string{"definition_json"},
			},
			{
				// Same definition, different key order and whitespace. This
				// must produce NO diff: definition_json is compared
				// semantically, and if that broke, every plan would show a
				// change that no apply could settle.
				Config: fmt.Sprintf(`
resource "yottabot_workflow" "test" {
  name    = %q
  status  = "draft"
  trigger = "manual"

  definition_json = jsonencode({
    steps = [{
      output = "result"
      type   = "agent_call"
      name   = "noop"
    }]
  })
}
`, name),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
		},
	})
}

func TestAccMCPGatewayAndTool_lifecycle(t *testing.T) {
	gwName := accName(t, "gw")
	toolName := accName(t, "tool")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "yottabot_mcp_gateway" "test" {
  name      = %q
  endpoint  = "https://mcp.example.com/acc"
  transport = "streamable-http"
  status    = "draft"
}

resource "yottabot_mcp_tool" "test" {
  name   = %q
  status = "draft"
  vendor = "YottaBot"

  config_json = jsonencode({
    gateway_id = yottabot_mcp_gateway.test.id
  })
}
`, gwName, toolName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("yottabot_mcp_gateway.test", "id"),
					resource.TestCheckResourceAttrSet("yottabot_mcp_tool.test", "id"),
					// vendor is the Terraform-side name; the wire field is
					// `provider`, which Terraform reserves. A round trip
					// reading back the value proves the mapping both ways.
					resource.TestCheckResourceAttr("yottabot_mcp_tool.test", "vendor", "YottaBot"),
				),
			},
			{
				ResourceName:      "yottabot_mcp_gateway.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:      "yottabot_mcp_tool.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// The Context provider is the one resource whose destroy is not a delete: the
// route retires the row, which keeps holding UNIQUE (account, type,
// external_id). This test therefore uses a per-run external_id and does NOT
// attempt destroy-then-recreate — that is known to fail, and asserting it
// passes would be asserting something the API cannot do.
//
// This test failed when first written, on a server defect rather than anything
// here: POST and PATCH returned 500 while still writing the row, because their
// RETURNING clauses selected 16 columns to a scan expecting 19. The row was
// created and the caller told it was not, so a run left orphaned handles the
// next apply then collided with. Fixed server-side by sharing one column list;
// this test is what proves it stays fixed from the client's side.
func TestAccContextProvider_lifecycle(t *testing.T) {
	external := accName(t, "ctx")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "yottabot_context_provider" "test" {
  type         = "github_org"
  external_id  = %q
  display_name = "acceptance suite"
  discoverer   = "github"
}
`, external),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("yottabot_context_provider.test", "external_id", external),
					resource.TestCheckResourceAttrSet("yottabot_context_provider.test", "id"),
				),
			},
			{
				ResourceName:      "yottabot_context_provider.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// display_name is updatable in place; type/external_id/
				// discoverer are replace-only and deliberately not touched
				// here, because replacing would hit the retirement trap.
				Config: fmt.Sprintf(`
resource "yottabot_context_provider" "test" {
  type         = "github_org"
  external_id  = %q
  display_name = "acceptance suite (updated)"
  discoverer   = "github"
}
`, external),
				Check: resource.TestCheckResourceAttr(
					"yottabot_context_provider.test", "display_name", "acceptance suite (updated)"),
			},
		},
	})
}

// TestAccGuardrailPolicy_lifecycle covers the two behaviours that are specific
// to this resource and cannot be proven by unit tests, because both live in the
// service rather than in the provider.
//
// Step 3 removes description and tags from the config. The service PATCHes with
// COALESCE, so an absent field preserves — the provider has to send an explicit
// empty string, and a following plan must be EMPTY. A non-empty plan here means
// the removal never reached the database.
//
// Steps 4 and 5 destroy the policy and recreate it under the SAME name. Delete
// is a soft delete, so the name has to be released when the row is retained.
// Without that, this fails with a duplicate key and a destroyed name is burned
// forever. This step is the regression test for that guarantee.
func TestAccGuardrailPolicy_lifecycle(t *testing.T) {
	name := accName(t, "guardrail")

	withFields := fmt.Sprintf(`
resource "yottabot_guardrail_policy" "test" {
  name        = %q
  description = "created by the provider acceptance suite"
  tags        = "acc,suite"
  definition  = jsonencode({ max_tokens = 1000 })
}
`, name)

	// Same name, and description/tags simply gone.
	withoutFields := fmt.Sprintf(`
resource "yottabot_guardrail_policy" "test" {
  name = %q
}
`, name)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: withFields,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("yottabot_guardrail_policy.test", "name", name),
					resource.TestCheckResourceAttr("yottabot_guardrail_policy.test", "description",
						"created by the provider acceptance suite"),
					resource.TestCheckResourceAttr("yottabot_guardrail_policy.test", "tags", "acc,suite"),
					resource.TestCheckResourceAttrSet("yottabot_guardrail_policy.test", "id"),
				),
			},
			{
				ResourceName:      "yottabot_guardrail_policy.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Remove from config -> apply -> plan clean. The framework
				// fails the step if the follow-up plan is non-empty, which is
				// exactly the assertion wanted: the clear reached the service
				// and the empty value read back as absent.
				Config: withoutFields,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("yottabot_guardrail_policy.test", "description"),
					resource.TestCheckNoResourceAttr("yottabot_guardrail_policy.test", "tags"),
					// definition is Optional AND Computed, so removing it from
					// config does NOT reset it — Terraform keeps the last
					// applied value, which is what Computed means. That is
					// standard framework behaviour and cannot be overridden
					// from the provider side; to clear it, set it explicitly to
					// jsonencode({}) (the step below does).
					//
					// An earlier version of this check expected "{}" here and
					// the docs claimed the same. The acceptance run is what
					// showed the claim was false.
					resource.TestCheckResourceAttr("yottabot_guardrail_policy.test",
						"definition", `{"max_tokens":1000}`),
				),
			},
			{
				// The supported way to clear a Computed attribute: say so
				// explicitly. This DOES reach the service, and the follow-up
				// plan must be empty.
				Config: fmt.Sprintf(`
resource "yottabot_guardrail_policy" "test" {
  name       = %q
  definition = jsonencode({})
}
`, name),
				Check: resource.TestCheckResourceAttr(
					"yottabot_guardrail_policy.test", "definition", "{}"),
			},
			{
				// Destroy, leaving the soft-deleted row behind holding the name.
				Config:  `# intentionally empty: destroys everything above`,
				Destroy: true,
			},
			{
				// Recreate under the same name. Without name release this fails with
				// `duplicate key value violates unique constraint`.
				Config: withFields,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("yottabot_guardrail_policy.test", "name", name),
					resource.TestCheckResourceAttrSet("yottabot_guardrail_policy.test", "id"),
				),
			},
		},
	})
}

// TestAccRole_lifecycle covers the one behaviour unit tests cannot reach: the
// service's COALESCE update. Step 2 removes `description` from config, and the
// following plan must be EMPTY — a non-empty plan means the provider preserved
// rather than cleared, and the config would never converge.
func TestAccRole_lifecycle(t *testing.T) {
	name := accName(t, "role")

	withDescription := fmt.Sprintf(`
resource "yottabot_role" "test" {
  name        = %q
  description = "created by the provider acceptance suite"
}
`, name)

	withoutDescription := fmt.Sprintf(`
resource "yottabot_role" "test" {
  name = %q
}
`, name)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: withDescription,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("yottabot_role.test", "name", name),
					resource.TestCheckResourceAttrSet("yottabot_role.test", "id"),
					// A fresh role has nothing attached; the counts are computed
					// on every read, so they must come back as real zeroes rather
					// than unknown.
					resource.TestCheckResourceAttr("yottabot_role.test", "policies", "0"),
					resource.TestCheckResourceAttr("yottabot_role.test", "groups", "0"),
					resource.TestCheckResourceAttr("yottabot_role.test", "users", "0"),
				),
			},
			{
				ResourceName:      "yottabot_role.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: withoutDescription,
				Check: resource.TestCheckNoResourceAttr(
					"yottabot_role.test", "description"),
			},
			{
				Config:   withoutDescription,
				PlanOnly: true,
			},
		},
	})
}

// TestAccGroup_lifecycle covers the permission replace-set, which is the whole
// point of this resource and lives entirely in the service.
//
// The permission strings here are REAL ones, taken from
// GET /v1/identity/permissions — the service validates against a canonical list
// of 226 and rejects anything else with a 400. An earlier version of this test
// used plausible-looking names (`agents:read`) that do not exist, which no unit
// test could have caught.
//
// Step 3 shrinks the set and step 4 empties it. Emptying is the interesting
// one: the service reads an absent `permissions` key as "leave alone", so an
// empty set has to reach the wire as `[]` rather than being omitted. A
// non-empty plan afterwards means it was dropped somewhere between config and
// request body.
func TestAccGroup_lifecycle(t *testing.T) {
	name := accName(t, "group")

	cfg := func(perms string) string {
		return fmt.Sprintf(`
resource "yottabot_group" "test" {
  name        = %q
  description = "created by the provider acceptance suite"
  permissions = %s
}
`, name, perms)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg(`["agent_runs:read", "agent_templates:read"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("yottabot_group.test", "name", name),
					resource.TestCheckResourceAttr("yottabot_group.test", "permissions.#", "2"),
					resource.TestCheckResourceAttr("yottabot_group.test", "is_builtin", "false"),
				),
			},
			{
				ResourceName:      "yottabot_group.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: cfg(`["agent_runs:read"]`),
				Check: resource.TestCheckResourceAttr(
					"yottabot_group.test", "permissions.#", "1"),
			},
			{
				Config: cfg(`[]`),
				Check: resource.TestCheckResourceAttr(
					"yottabot_group.test", "permissions.#", "0"),
			},
			{
				Config:   cfg(`[]`),
				PlanOnly: true,
			},
		},
	})
}

// TestAccMachineGroup_lifecycle covers the pointer-presence update dialect:
// step 3 removes `description`, and the following plan must be EMPTY. The
// handler skips an absent field, so the provider has to send an explicit ""
// or the removal never reaches the database.
func TestAccMachineGroup_lifecycle(t *testing.T) {
	name := accName(t, "machinegroup")

	withDescription := fmt.Sprintf(`
resource "yottabot_machine_group" "test" {
  name        = %q
  description = "created by the provider acceptance suite"
}
`, name)

	withoutDescription := fmt.Sprintf(`
resource "yottabot_machine_group" "test" {
  name = %q
}
`, name)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: withDescription,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("yottabot_machine_group.test", "name", name),
					resource.TestCheckResourceAttr("yottabot_machine_group.test", "is_builtin", "false"),
					// A fresh group has no members, and the count is computed on
					// read — it must come back as a real zero, not unknown.
					resource.TestCheckResourceAttr("yottabot_machine_group.test", "member_count", "0"),
				),
			},
			{
				ResourceName:      "yottabot_machine_group.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: withoutDescription,
				Check:  resource.TestCheckNoResourceAttr("yottabot_machine_group.test", "description"),
			},
			{
				Config:   withoutDescription,
				PlanOnly: true,
			},
		},
	})
}

// TestAccPolicy_lifecycle covers what only a live server can show: that
// statements are genuinely editable in place.
//
// Before the statements-editable PATCH they were create-only, so changing one
// meant destroying the policy — and role_policy_attachments is ON DELETE
// CASCADE, which silently detached it from every role. Step 3 is the proof the
// destroy no longer happens: the id must be UNCHANGED across a statement edit.
//
// Step 5 empties the list. The server leaves statements alone when the field is
// absent, so an empty list has to reach the wire as `[]`; a following empty plan
// is what shows it did.
func TestAccPolicy_lifecycle(t *testing.T) {
	name := accName(t, "policy")

	cfg := func(statements string) string {
		return fmt.Sprintf(`
resource "yottabot_policy" "test" {
  name        = %q
  description = "created by the provider acceptance suite"
%s
}
`, name, statements)
	}

	oneStatement := cfg(`
  statements = [
    {
      sid     = "read-agents"
      effect  = "allow"
      actions = ["agents:read"]
    },
  ]`)

	// Same statements, a deny prepended. Order is evaluation order, so this is
	// a different policy — not a reordering the server may normalize away.
	twoStatements := cfg(`
  statements = [
    {
      sid     = "deny-writes"
      effect  = "deny"
      actions = ["agents:write", "agents:delete"]
    },
    {
      sid       = "read-agents"
      effect    = "allow"
      actions   = ["agents:read"]
      resources = ["*"]
    },
  ]`)

	noStatements := cfg(`  statements = []`)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: oneStatement,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("yottabot_policy.test", "name", name),
					resource.TestCheckResourceAttr("yottabot_policy.test", "statements.#", "1"),
					// Never `system`: that kind refuses update and delete, so a
					// policy created as one could never be managed or destroyed.
					resource.TestCheckResourceAttr("yottabot_policy.test", "kind", "custom"),
					// Attached to nothing yet, and read-only.
					resource.TestCheckResourceAttr("yottabot_policy.test", "attached", ""),
				),
			},
			{
				ResourceName:      "yottabot_policy.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: twoStatements,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("yottabot_policy.test", "statements.#", "2"),
					// Order is preserved and is the evaluation order.
					resource.TestCheckResourceAttr("yottabot_policy.test", "statements.0.effect", "deny"),
					resource.TestCheckResourceAttr("yottabot_policy.test", "statements.1.effect", "allow"),
				),
			},
			{
				Config:   twoStatements,
				PlanOnly: true,
			},
			{
				Config: noStatements,
				Check: resource.TestCheckResourceAttr(
					"yottabot_policy.test", "statements.#", "0"),
			},
			{
				Config:   noStatements,
				PlanOnly: true,
			},
		},
	})
}

// TestAccLLMGateway_lifecycle covers the two things unit tests can only assert
// structurally.
//
// Step 3 removes `description` and `endpoint`. The service PATCHes with
// COALESCE, so absent preserves — the provider must send an explicit empty
// string, and the follow-up plan must be EMPTY.
//
// Steps 4-5 change `upstream_provider`. The update route does not accept the
// field at all — the service says "Provider is intentionally not updatable
// (changing it is a new gateway)" — so without RequiresReplace Terraform would
// plan an in-place update the API silently ignores, and the resource would
// never converge. The assertion is that the ID CHANGES: that is what proves a
// replacement happened rather than a no-op update.
func TestAccLLMGateway_lifecycle(t *testing.T) {
	name := accName(t, "gateway")

	cfg := func(provider, extra string) string {
		return fmt.Sprintf(`
resource "yottabot_llm_gateway" "test" {
  name              = %q
  upstream_provider = %q
%s
}
`, name, provider, extra)
	}

	full := cfg("anthropic", `  description = "created by the provider acceptance suite"
  endpoint    = "https://example.invalid/v1"`)
	trimmed := cfg("anthropic", "")
	replaced := cfg("openai", "")

	var firstID, secondID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: full,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("yottabot_llm_gateway.test", "name", name),
					resource.TestCheckResourceAttr("yottabot_llm_gateway.test", "upstream_provider", "anthropic"),
					// vendor is the gateway's OWNER — a different field from the
					// upstream it calls, which is why this resource exposes the
					// latter as `upstream_provider` rather than reusing the
					// `vendor` name the way yottabot_mcp_tool did.
					//
					// It is EMPTY here, and that is correct: vendor identifies
					// the implementer of a managed catalog gateway, and this is
					// a customer-registered one. Asserting it is set was wrong
					// and the acceptance run caught it.
					resource.TestCheckNoResourceAttr("yottabot_llm_gateway.test", "vendor"),
					captureAttr("yottabot_llm_gateway.test", "id", &firstID),
				),
			},
			{
				ResourceName:      "yottabot_llm_gateway.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: trimmed,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("yottabot_llm_gateway.test", "description"),
					resource.TestCheckNoResourceAttr("yottabot_llm_gateway.test", "endpoint"),
				),
			},
			{
				Config:   trimmed,
				PlanOnly: true,
			},
			{
				Config: replaced,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("yottabot_llm_gateway.test", "upstream_provider", "openai"),
					captureAttr("yottabot_llm_gateway.test", "id", &secondID),
					func(*terraform.State) error {
						if firstID == "" || secondID == "" {
							return fmt.Errorf("ids not captured: %q → %q", firstID, secondID)
						}
						if firstID == secondID {
							return fmt.Errorf("id is unchanged (%s) after changing upstream_provider — "+
								"the resource was updated in place, which the API silently ignores, "+
								"so RequiresReplace is not doing its job", firstID)
						}
						return nil
					},
				),
			},
		},
	})
}

// captureAttr stashes an attribute value so a later step can compare against it
// — the only way to assert that a change forced a replacement rather than an
// in-place update.
func captureAttr(resourceName, attr string, into *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource %s not in state", resourceName)
		}
		v, ok := rs.Primary.Attributes[attr]
		if !ok {
			return fmt.Errorf("%s has no attribute %q", resourceName, attr)
		}
		*into = v
		return nil
	}
}

// TestAccPrompt_lifecycle proves the two behaviours that only a live service
// shows, and that a unit test can only approximate.
//
// Step 3 edits the DESCRIPTION ONLY. That must succeed: the provider omits the
// unchanged version, and re-sending it would ask the service to publish 1.0.0 a
// second time — `UNIQUE (prompt_id, version)` makes that a duplicate key on an
// edit that never touched the content.
//
// Step 4 edits the body WITH a version bump, which publishes. The prompt id must
// be UNCHANGED across it: a new version is not a new prompt.
func TestAccPrompt_lifecycle(t *testing.T) {
	name := accName(t, "prompt")
	var firstID, afterPublishID string

	cfg := func(version, body, desc string) string {
		return fmt.Sprintf(`
resource "yottabot_prompt" "test" {
  name        = %q
  description = %q
  version     = %q
  body        = %q
  variables   = ["subject"]
}
`, name, desc, version, body)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg("1.0.0", "Summarise {{subject}}.", "first"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("yottabot_prompt.test", "version", "1.0.0"),
					resource.TestCheckResourceAttr("yottabot_prompt.test", "used_by", "0"),
					captureAttr("yottabot_prompt.test", "id", &firstID),
				),
			},
			{
				ResourceName:      "yottabot_prompt.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Description only — must NOT republish 1.0.0.
				Config: cfg("1.0.0", "Summarise {{subject}}.", "second"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("yottabot_prompt.test", "description", "second"),
					resource.TestCheckResourceAttr("yottabot_prompt.test", "version", "1.0.0"),
				),
			},
			{
				Config:   cfg("1.0.0", "Summarise {{subject}}.", "second"),
				PlanOnly: true,
			},
			{
				// A real publish.
				Config: cfg("1.1.0", "Summarise {{subject}} in one line.", "second"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("yottabot_prompt.test", "version", "1.1.0"),
					captureAttr("yottabot_prompt.test", "id", &afterPublishID),
					func(*terraform.State) error {
						if firstID == "" || afterPublishID == "" {
							return fmt.Errorf("ids not captured: %q → %q", firstID, afterPublishID)
						}
						if firstID != afterPublishID {
							return fmt.Errorf("the prompt id changed (%s → %s) — publishing a new "+
								"version must not destroy and recreate the prompt, which would lose "+
								"its history and every reference to it", firstID, afterPublishID)
						}
						return nil
					},
				),
			},
		},
	})
}

// TestAccServiceAccount_lifecycle covers username reuse after retirement.
//
// Step 4 destroys and step 5 recreates under the SAME username. Retirement keeps
// the row, and until uniqueness was narrowed to exclude retired service accounts
// the name was burned forever — this failed on a duplicate key, which made
// `terraform destroy` a one-way door.
//
// The owning group is supplied via YOTTABOT_ACC_GROUP_ID rather than created
// here, and that is not laziness. A retired service account KEEPS its
// owner_group_id, and human_users.owner_group_id is ON DELETE RESTRICT, so a
// group that has owned one can never be deleted — a config that creates both
// fails on every destroy. That interaction is a real product question (see the
// 409 this now returns instead of a 500), and it is not what this test is about.
func TestAccServiceAccount_lifecycle(t *testing.T) {
	groupID := os.Getenv("YOTTABOT_ACC_GROUP_ID")
	if groupID == "" {
		t.Skip("set YOTTABOT_ACC_GROUP_ID to an existing group's UUID — a service account " +
			"needs an owner group, and creating one here would make the test's own destroy " +
			"fail on the RESTRICT that a retired account keeps alive")
	}
	username := accName(t, "svcacct")

	cfg := fmt.Sprintf(`
resource "yottabot_service_account" "test" {
  username       = %q
  owner_group_id = %q
}
`, username, groupID)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("yottabot_service_account.test", "username", username),
					resource.TestCheckResourceAttr("yottabot_service_account.test", "status", "active"),
					resource.TestCheckResourceAttrSet("yottabot_service_account.test", "owner_group_name"),
					// No credential ever reaches state.
					resource.TestCheckNoResourceAttr("yottabot_service_account.test", "private_key_pem"),
				),
			},
			{
				ResourceName:      "yottabot_service_account.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:   cfg,
				PlanOnly: true,
			},
			{
				Config:  `# intentionally empty: destroys everything above`,
				Destroy: true,
			},
			{
				// Same username again. Without the narrowed uniqueness this
				// fails with `duplicate key value violates unique constraint`.
				Config: cfg,
				Check: resource.TestCheckResourceAttr(
					"yottabot_service_account.test", "username", username),
			},
		},
	})
}
