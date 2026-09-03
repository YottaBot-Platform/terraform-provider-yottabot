package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
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
// is a soft delete, and until bot/268 the table's UNIQUE (account_id, name)
// covered deleted rows too, so this failed with a duplicate key and a destroyed
// name was burned forever. This step is the regression test for that migration.
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
					// definition's column is NOT NULL DEFAULT '{}', so removing
					// it resets rather than clears.
					resource.TestCheckResourceAttr("yottabot_guardrail_policy.test", "definition", "{}"),
				),
			},
			{
				// Destroy, leaving the soft-deleted row behind holding the name.
				Config:  `# intentionally empty: destroys everything above`,
				Destroy: true,
			},
			{
				// Recreate under the same name. Before bot/268 this failed with
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
				Config: cfg(`["agents:read", "workflows:read"]`),
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
				Config: cfg(`["agents:read"]`),
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
