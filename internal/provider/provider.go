// Package provider implements the YottaBot Terraform provider.
//
// Scope of v1 is five existing REST-backed resources — agent, workflow,
// context provider, MCP gateway, MCP tool. The provider deliberately wraps
// routes that already exist and are already permission-gated rather than
// introducing a provider-specific API.
package provider

import (
	"context"
	"fmt"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

// Ensure the implementation satisfies the framework interface at compile time
// rather than at `terraform plan`.
var _ provider.Provider = (*yottabotProvider)(nil)

type yottabotProvider struct {
	version string
}

// New returns the provider factory main.go serves and acceptance tests mount.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &yottabotProvider{version: version}
	}
}

func (p *yottabotProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	// TypeName drives every resource name: `yottabot` + `_agent` =
	// `yottabot_agent`. Changing it renames every resource in every
	// customer's state.
	resp.TypeName = "yottabot"
	resp.Version = p.version
}

// providerModel mirrors the provider block. Every field is Optional because
// each one can come from the environment instead; Validate reports what is
// actually missing after resolution, which is the only point where that is
// knowable.
type providerModel struct {
	Endpoint      types.String `tfsdk:"endpoint"`
	Token         types.String `tfsdk:"token"`
	UserID        types.String `tfsdk:"user_id"`
	KID           types.String `tfsdk:"kid"`
	PrivateKeyPEM types.String `tfsdk:"private_key_pem"`
	TokenURL      types.String `tfsdk:"token_url"`
}

func (p *yottabotProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage YottaBot agents, workflows, Context providers, and MCP catalog rows.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "YottaBot API base URL, e.g. `https://yottabot.example.com`. Falls back to `YOTTABOT_ENDPOINT`.",
			},
			"token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				MarkdownDescription: "Bearer token for manual or local runs. Falls back to `YOTTABOT_TOKEN`. " +
					"Prefer service-account client credentials for automation — with a PAT, the audit trail says a human ran Terraform.",
			},
			"user_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Service-account user id (OAuth client credentials). Falls back to `YOTTABOT_USER_ID`.",
			},
			"kid": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Service-account key id. Falls back to `YOTTABOT_KID`.",
			},
			"private_key_pem": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "Service-account private key, PEM encoded. Falls back to `YOTTABOT_PRIVATE_KEY_PEM`. Never written to state.",
			},
			"token_url": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "OAuth token endpoint. Falls back to `YOTTABOT_TOKEN_URL`, then to `<endpoint>" +
					defaultTokenPath + "`.",
			},
		},
	}
}

func (p *yottabotProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// An unknown value here means it comes from another resource's not-yet-known
	// output. Terraform will call Configure again after apply, so defer rather
	// than reporting a bogus "missing endpoint".
	if cfg.Endpoint.IsUnknown() || cfg.Token.IsUnknown() || cfg.PrivateKeyPEM.IsUnknown() {
		return
	}

	settings := ResolveSettings(Settings{
		Endpoint:      cfg.Endpoint.ValueString(),
		Token:         cfg.Token.ValueString(),
		UserID:        cfg.UserID.ValueString(),
		KID:           cfg.KID.ValueString(),
		PrivateKeyPEM: cfg.PrivateKeyPEM.ValueString(),
		TokenURL:      cfg.TokenURL.ValueString(),
	}, os.LookupEnv)

	// Warn before erroring: a half-set service account is the confusing case,
	// because it otherwise degrades silently to token auth and the practitioner
	// sees permission errors rather than a configuration problem.
	if settings.PartialServiceAccount() {
		resp.Diagnostics.AddWarning(
			"Incomplete service-account credentials",
			"Set all of `user_id`, `kid`, and `private_key_pem` to use service-account authentication. "+
				"With only some of them set, the provider falls back to `token` if present.",
		)
	}
	for _, err := range settings.Validate() {
		resp.Diagnostics.AddError("Invalid provider configuration", err.Error())
	}
	if resp.Diagnostics.HasError() {
		return
	}

	c, err := newClient(settings)
	if err != nil {
		// Credential material is validated here rather than on the first API
		// call, where a malformed key would surface as a 401 and read as a
		// permissions problem.
		resp.Diagnostics.AddError("Invalid provider credentials", err.Error())
		return
	}
	resp.ResourceData = c
	resp.DataSourceData = c
}

// newClient builds the REST client for the credential path these settings
// select. Settings.Validate has already ruled out AuthNone.
func newClient(s Settings) (*client.Client, error) {
	var (
		tokens client.TokenSource
		err    error
	)
	switch s.Mode() {
	case AuthServiceAccount:
		tokens, err = client.NewServiceAccountTokenSource(
			s.UserID, s.KID, s.PrivateKeyPEM, s.TokenURL, nil)
		if err != nil {
			return nil, err
		}
	case AuthToken:
		tokens = client.NewStaticTokenSource(s.Token)
	default:
		return nil, fmt.Errorf("no credentials configured")
	}
	return client.New(s.Endpoint, tokens, nil), nil
}

// Resources returns the managed resources. Steps 6-8 add workflow, context
// provider, and the two MCP resources alongside the agent.
func (p *yottabotProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewAgentResource,
		NewWorkflowResource,
		NewContextProviderResource,
		NewMCPGatewayResource,
		NewMCPToolResource,
		NewGuardrailPolicyResource,
		NewLLMGatewayResource,
		NewRoleResource,
		NewGroupResource,
		NewMachineGroupResource,
		NewPolicyResource,
	}
}

func (p *yottabotProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}
