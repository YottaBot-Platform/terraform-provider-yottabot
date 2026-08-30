package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// These drive the validators through the interfaces Terraform actually calls,
// rather than through the helper each one wraps. The helpers are tested
// elsewhere; what these add is proof the validator is wired — that it reads
// ConfigValue, attaches the diagnostic to the right attribute path, and knows
// when to stay silent.

func stringReq(v types.String) validator.StringRequest {
	return validator.StringRequest{Path: path.Root("status"), ConfigValue: v}
}

func TestOneOfValidator_AcceptsAnAllowedValue(t *testing.T) {
	var resp validator.StringResponse
	oneOfValidator([]string{"draft", "available"}).
		ValidateString(context.Background(), stringReq(types.StringValue("draft")), &resp)

	if resp.Diagnostics.HasError() {
		t.Errorf("allowed value rejected: %v", resp.Diagnostics)
	}
}

func TestOneOfValidator_RejectsAndNamesTheAttribute(t *testing.T) {
	var resp validator.StringResponse
	oneOfValidator([]string{"draft", "available"}).
		ValidateString(context.Background(), stringReq(types.StringValue("published")), &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("invalid value accepted")
	}
	d := resp.Diagnostics.Errors()[0]
	// The message must list the allowed values. A validator that only says
	// "invalid" makes the practitioner go read our source.
	if !strings.Contains(d.Detail(), "draft") || !strings.Contains(d.Detail(), "available") {
		t.Errorf("error does not list the vocabulary: %q", d.Detail())
	}
	if !strings.Contains(d.Detail(), "published") {
		t.Errorf("error does not quote the offending value: %q", d.Detail())
	}
}

// A value that is null or not yet known must pass. Terraform runs validators
// during plan, when a value interpolated from another resource is still
// unknown — erroring there would fail every plan that references one.
func TestOneOfValidator_StaysSilentOnNullAndUnknown(t *testing.T) {
	for name, v := range map[string]types.String{
		"null":    types.StringNull(),
		"unknown": types.StringUnknown(),
	} {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			oneOfValidator([]string{"draft"}).
				ValidateString(context.Background(), stringReq(v), &resp)
			if resp.Diagnostics.HasError() {
				t.Errorf("%s value rejected: %v", name, resp.Diagnostics)
			}
		})
	}
}

func mapReq(t *testing.T, m map[string]attr.Value) validator.MapRequest {
	t.Helper()
	v, diags := types.MapValue(types.StringType, m)
	if diags.HasError() {
		t.Fatalf("building map: %v", diags)
	}
	return validator.MapRequest{Path: path.Root("env"), ConfigValue: v}
}

func TestEnvKeyValidator_AcceptsConventionalNames(t *testing.T) {
	var resp validator.MapResponse
	envKeyValidator{}.ValidateMap(context.Background(), mapReq(t, map[string]attr.Value{
		"LOG_LEVEL": types.StringValue("info"),
		"_PRIVATE":  types.StringValue("x"),
		"A1":        types.StringValue("y"),
	}), &resp)

	if resp.Diagnostics.HasError() {
		t.Errorf("valid env names rejected: %v", resp.Diagnostics)
	}
}

func TestEnvKeyValidator_RejectsLowercaseAndPunctuation(t *testing.T) {
	for _, key := range []string{"lower", "HAS-DASH", "1LEADING", "HAS.DOT", "HAS SPACE"} {
		t.Run(key, func(t *testing.T) {
			var resp validator.MapResponse
			envKeyValidator{}.ValidateMap(context.Background(), mapReq(t, map[string]attr.Value{
				key: types.StringValue("v"),
			}), &resp)

			if !resp.Diagnostics.HasError() {
				t.Errorf("%q accepted — the platform would reject it at apply", key)
			}
		})
	}
}

// Every bad key should be reported, not just the first. A validator that stops
// at the first turns one fix into several apply cycles.
func TestEnvKeyValidator_ReportsEveryBadKey(t *testing.T) {
	var resp validator.MapResponse
	envKeyValidator{}.ValidateMap(context.Background(), mapReq(t, map[string]attr.Value{
		"bad-one":   types.StringValue("v"),
		"bad.two":   types.StringValue("v"),
		"GOOD_NAME": types.StringValue("v"),
	}), &resp)

	if got := len(resp.Diagnostics.Errors()); got != 2 {
		t.Errorf("reported %d bad keys, want 2 — %v", got, resp.Diagnostics)
	}
}

func TestEnvKeyValidator_StaysSilentOnNullAndUnknown(t *testing.T) {
	for name, v := range map[string]types.Map{
		"null":    types.MapNull(types.StringType),
		"unknown": types.MapUnknown(types.StringType),
	} {
		t.Run(name, func(t *testing.T) {
			var resp validator.MapResponse
			envKeyValidator{}.ValidateMap(context.Background(),
				validator.MapRequest{Path: path.Root("env"), ConfigValue: v}, &resp)
			if resp.Diagnostics.HasError() {
				t.Errorf("%s map rejected: %v", name, resp.Diagnostics)
			}
		})
	}
}

// Descriptions surface in `terraform validate` output and in generated docs, so
// an empty one is a silent documentation gap.
func TestValidatorDescriptions_AreNotEmpty(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct{ desc, md string }{
		"oneOf": {
			oneOfValidator([]string{"a", "b"}).Description(ctx),
			oneOfValidator([]string{"a", "b"}).MarkdownDescription(ctx),
		},
		"envKey": {
			envKeyValidator{}.Description(ctx),
			envKeyValidator{}.MarkdownDescription(ctx),
		},
		"pollSourceConflict": {
			pollSourceConflict{}.Description(ctx),
			pollSourceConflict{}.MarkdownDescription(ctx),
		},
	}
	for name, c := range cases {
		if strings.TrimSpace(c.desc) == "" {
			t.Errorf("%s: empty Description", name)
		}
		if strings.TrimSpace(c.md) == "" {
			t.Errorf("%s: empty MarkdownDescription", name)
		}
	}
}
