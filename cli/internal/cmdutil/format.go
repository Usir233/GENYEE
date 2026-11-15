package cmdutil

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/itchyny/gojq"
	"github.com/spf13/cobra"

	"github.com/Usir233/GENYEE/cli/internal/config"
	"github.com/Usir233/GENYEE/cli/internal/format"
	"github.com/Usir233/GENYEE/cli/internal/output"
)

// FormatMode is the resolved --format value (typed enum).
type FormatMode string

const (
	FormatText   FormatMode = "text"
	FormatJSON   FormatMode = "json"
	FormatNDJSON FormatMode = "ndjson"
)

// DefaultFormatMode is the mode used when neither --format nor GENYEE_FORMAT
// is set. Single source of truth shared by FormatOptions.ResolveDefault and
// cmd.resolveFormatEarly (the early cobra-parse-error path) so the two cannot
// drift on what "no flag" defaults to.
const DefaultFormatMode = FormatJSON

// FormatOptions captures the resolved --format + --jq state for a command.
// Mode is one of FormatText / FormatJSON / FormatNDJSON, or "" before
// ResolveDefault has been called.
type FormatOptions struct {
	Mode FormatMode
	JQ   string
	TTY  bool // ResolveDefault populates; Emit reads for indent decision
}

// AddFormatFlag attaches the --jq projection field hints (in cmd.Long) for
// commands that honor --format. The flags themselves are registered as
// persistent globals at the root, so this helper no longer re-registers
// them — it only appends documentation. Callers that don't need field
// hints can skip this call entirely.
func AddFormatFlag(cmd *cobra.Command, fieldHints ...string) {
	if len(fieldHints) > 0 {
		sorted := append([]string(nil), fieldHints...)
		sort.Strings(sorted)
		// Fields live under .data in the {ok,data,meta} envelope, so --jq must
		// be rooted there: `--jq '.data.<field>'` (object) or
		// `--jq '.data[].<field>'` (list). A bare `--jq '.<field>'` matches the
		// envelope top level and silently returns null — spell out the path so
		// agents don't ship broken projections.
		hdr := "\n\nJSON fields available under .data (project with --jq '.data.<field>', or '.data[].<field>' for lists):\n  " +
			strings.Join(sorted, "\n  ")
		if cmd.Long != "" {
			cmd.Long += hdr
		} else {
			cmd.Long = strings.TrimSpace(cmd.Short) + hdr
		}
	}
}

// CheckFormatFlag resolves --format + --jq from cmd. Returns:
//   - (*FormatOptions{Mode:""}, nil)        flag not set; caller should call ResolveDefault
//   - (*FormatOptions{Mode:v,JQ:q}, nil)    valid values
//   - (nil, *FlagError)                     invalid --format, or --jq with explicit --format text
//
// --jq with --format unset is accepted: ResolveDefault below will promote
// the mode to FormatJSON so the filter has somewhere to apply.
func CheckFormatFlag(cmd *cobra.Command) (*FormatOptions, error) {
	fopts := &FormatOptions{}
	if f := cmd.Flags().Lookup("format"); f != nil {
		v := f.Value.String()
		switch v {
		case "":
			// unset; caller calls ResolveDefault
		case "text", "json", "ndjson":
			fopts.Mode = FormatMode(v)
		default:
			return nil, NewFlagError(fmt.Errorf("invalid --format %q: must be text | json | ndjson", v))
		}
	}
	if f := cmd.Flags().Lookup("jq"); f != nil {
		fopts.JQ = f.Value.String()
	}
	// --jq only makes sense for JSON-shaped output. Validate the effective
	// mode before syntax so text selected by the environment has the same
	// format error priority as an explicit text flag.
	effectiveMode := fopts.Mode
	if effectiveMode == "" {
		effectiveMode = FormatMode(os.Getenv("GENYEE_FORMAT"))
	}
	if fopts.JQ != "" && effectiveMode == FormatText {
		return nil, NewFlagError(errors.New("--jq requires --format json|ndjson"))
	}
	if fopts.JQ != "" {
		if _, err := gojq.Parse(fopts.JQ); err != nil {
			return nil, mapJQError(&format.JQError{Err: fmt.Errorf("jq parse: %w", err)})
		}
	}
	return fopts, nil
}

// WantsJSON reports whether the resolved mode is JSON or NDJSON. Used by
// callers to choose between the JSON emit path and text rendering.
func (o *FormatOptions) WantsJSON() bool {
	return o.Mode == FormatJSON || o.Mode == FormatNDJSON
}

// Emit serializes data wrapped in the success envelope (FormatJSON
// path) or as bare NDJSON lines (FormatNDJSON path). meta is optional
// (pass nil for mutation commands without batch counts).
//
// FormatJSON path: envelope is {ok:true, data:..., meta?:..., profile?:...}.
// Indent is determined by o.TTY (populated by ResolveDefault).
// When o.JQ is set, jq evaluates against the full envelope JSON, so users
// project with ".data[]", ".meta.count", etc.
//
// FormatNDJSON path: emits one bare JSON object per line (no envelope).
// Matches the NDJSON event-passthrough contract used by streaming commands.
//
// FormatText path returns an error so a missed dispatch surfaces loudly.
func (o *FormatOptions) Emit(w io.Writer, data any, meta *output.Meta) error {
	switch o.Mode {
	case FormatJSON:
		if o.JQ != "" {
			return mapJQError(format.WriteJSONFiltered(w, output.NewEnvelope(data, meta, globalProfile), nil, o.JQ))
		}
		return output.WriteEnvelope(w, data, meta, o.TTY, globalProfile)
	case FormatNDJSON:
		if o.JQ != "" {
			return mapJQError(format.WriteJSONFiltered(w, data, nil, o.JQ))
		}
		return format.WriteNDJSON(w, data)
	case FormatText:
		return fmt.Errorf("FormatOptions.Emit: cannot emit text mode as JSON; caller must render human-readable separately")
	default:
		return fmt.Errorf("FormatOptions.Emit: unknown mode %q", o.Mode)
	}
}

// mapJQError converts a failure rooted in the user-supplied --jq expression
// (format.JQError) into a typed input.invalid_argument error (exit 5) so agents
// fix the expression instead of treating a bad --jq as a CLI bug. Non-jq errors
// (e.g. an internal serialization fault) pass through unchanged.
func mapJQError(err error) error {
	if err == nil {
		return nil
	}
	var jqe *format.JQError
	if errors.As(err, &jqe) {
		return NewError(CodeInputInvalidArgument, jqe.Error()).
			WithHint("invalid --jq expression; see https://jqlang.github.io/jq/manual/")
	}
	return err
}

// ResolveConfiguredFormat returns the effective default output format when
// `--format` is not passed on the command line:
//  1. GENYEE_FORMAT env (if text | json | ndjson)
//  2. config.yaml defaults.format (if text | json | ndjson)
//  3. DefaultFormatMode (json)
func ResolveConfiguredFormat() FormatMode {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("GENYEE_FORMAT"))); v == "text" || v == "json" || v == "ndjson" {
		return FormatMode(v)
	}
	if cfg, err := config.Load(); err == nil && cfg != nil {
		if v := strings.ToLower(strings.TrimSpace(cfg.Defaults.Format)); v == "text" || v == "json" || v == "ndjson" {
			return FormatMode(v)
		}
	}
	return DefaultFormatMode
}

// ResolveDefault fills in Mode when the caller has not explicitly set it:
//   - Precedence: explicit --format > GENYEE_FORMAT > config defaults.format > FormatJSON
//   - When --jq is supplied without explicit --format / GENYEE_FORMAT, promotes a
//     configured text default to FormatJSON so the jq filter can execute.
//   - TTY only affects the indent decision (auto-indent in TTY; compact in pipe)
func (o *FormatOptions) ResolveDefault(tty bool) {
	o.TTY = tty
	o.FromEnv()
	if o.Mode != "" {
		return
	}
	configured := ResolveConfiguredFormat()
	if o.JQ != "" && configured == FormatText {
		o.Mode = FormatJSON
		return
	}
	o.Mode = configured
}

// FromEnv reads GENYEE_FORMAT and applies it when Mode hasn't been set
// by --format. ResolveDefault calls this first so GENYEE_FORMAT outranks
// config.yaml defaults.format.
//
// Invalid env values are silently ignored (the user's --format on a
// later invocation will still take precedence).
func (o *FormatOptions) FromEnv() {
	if o.Mode != "" {
		return
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv("GENYEE_FORMAT")))
	switch v {
	case "text", "json", "ndjson":
		o.Mode = FormatMode(v)
	}
}
