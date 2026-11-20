package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"unrent.dev/terraform-provider-unrent/internal/client"
)

// The API's identifier rules (apps/control/src/api-v1/routes*.ts), checked at plan time.
var (
	envRe       = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	nameRe      = regexp.MustCompile(`^[a-z][a-z0-9-]{1,40}$`)
	dbRe        = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	projectRe   = regexp.MustCompile(`^prj_[0-9a-z]{7,32}$`)
	userRe      = regexp.MustCompile(`^usr_[0-9a-z]{16,32}$`)
	agentRe     = regexp.MustCompile(`^agt_[0-9a-z]{16,32}$`)
	cronRe      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)
	hostRe      = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	storageRe   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
	variableRe  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)
	cronExprRe  = regexp.MustCompile(`^\S+( \S+){4}$`)
	channelIDRe = regexp.MustCompile(`^chn_[0-9a-f]{20}$`)
)

func envValidator() validator.String {
	return stringvalidator.RegexMatches(envRe, "2–31 lower-case letters, digits or hyphens, starting with a letter (e.g. production)")
}
func nameValidator() validator.String {
	return stringvalidator.RegexMatches(nameRe, "2–41 lower-case letters, digits or hyphens, starting with a letter")
}
func dbValidator() validator.String {
	return stringvalidator.RegexMatches(dbRe, "lower-case letters, digits and _, starting with a letter")
}
func projectValidator() validator.String {
	return stringvalidator.RegexMatches(projectRe, "a project id (prj_…)")
}
func agentValidator() validator.String {
	return stringvalidator.RegexMatches(agentRe, "a server id (agt_…, see the unrent_servers data source)")
}

// base is what every resource and data source shares: the configured API client.
type base struct {
	c *client.Client
}

func (b *base) configure(data any, diags *diag.Diagnostics) {
	if data == nil {
		return // the provider is not configured yet (validation)
	}
	c, ok := data.(*client.Client)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("%T", data))
		return
	}
	b.c = c
}

func (b *base) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	b.configure(req.ProviderData, &resp.Diagnostics)
}

type dsBase struct{ base }

func (b *dsBase) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	b.configure(req.ProviderData, &resp.Diagnostics)
}

// fail adds an error for err with a summary; held jobs say exactly where to approve them.
func fail(diags *diag.Diagnostics, summary string, err error) {
	var ae *client.ApprovalError
	if errors.As(err, &ae) {
		diags.AddError("Approval required: "+summary, ae.Error())
		return
	}
	diags.AddError(summary, err.Error())
}

// splitID splits an import id of n parts joined by "/".
func splitID(id string, n int, format string, diags *diag.Diagnostics) []string {
	parts := strings.Split(id, "/")
	if len(parts) != n {
		diags.AddError("Invalid import id", fmt.Sprintf("expected %s, got %q", format, id))
		return nil
	}
	for _, p := range parts {
		if p == "" {
			diags.AddError("Invalid import id", fmt.Sprintf("expected %s, got %q", format, id))
			return nil
		}
	}
	return parts
}

// str is a string attribute's value ("" when null or unknown).
func str(v types.String) string {
	if v.IsNull() || v.IsUnknown() {
		return ""
	}
	return v.ValueString()
}

// nullStr is a nullable API string as a Terraform value.
func nullStr(p *string) types.String {
	if p == nil {
		return types.StringNull()
	}
	return types.StringValue(*p)
}

// Deployment is the part of GET /deployments/{id} the provider waits on.
type deployment struct {
	ID    string  `json:"id"`
	State string  `json:"state"`
	Error *string `json:"error"`
	Steps []struct {
		Name  string  `json:"name"`
		JobID *string `json:"job_id"`
		State string  `json:"state"`
	} `json:"steps"`
}

// waitDeployment follows a deployment to its end: a step held for passkeys is waited for (approval_timeout) with
// its approval page; failed or cancelled is an error with the server's reason.
func waitDeployment(ctx context.Context, c *client.Client, id string) (*deployment, error) {
	if id == "" {
		return nil, nil
	}
	announced := map[string]bool{}
	for i := 0; ; i++ {
		var d deployment
		if err := c.Get(ctx, client.OpGetDeployment, client.Params{"deployment": id}, nil, &d); err != nil {
			return nil, err
		}
		switch d.State {
		case "succeeded":
			return &d, nil
		case "failed", "cancelled", "expired", "rejected":
			msg := d.State
			if d.Error != nil && *d.Error != "" {
				msg += ": " + *d.Error
			}
			return &d, fmt.Errorf("deployment %s %s", id, msg)
		case "pending_approval":
			for _, s := range d.Steps {
				if s.JobID != nil && s.State == "pending_approval" && !announced[*s.JobID] {
					announced[*s.JobID] = true
					if err := c.WaitApproval(ctx, *s.JobID); err != nil {
						return &d, err
					}
				}
			}
		}
		if err := c.Tick(ctx, i); err != nil {
			return &d, fmt.Errorf("deployment %s still %s: %w", id, d.State, err)
		}
	}
}

var objectAs = basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true, UnhandledUnknownAsEmpty: true}

func regexpMust(re string) *regexp.Regexp { return regexp.MustCompile(re) }

func errorf(f string, a ...any) error { return fmt.Errorf(f, a...) }
