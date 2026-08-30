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
