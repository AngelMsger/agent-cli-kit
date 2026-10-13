package config

import (
	"fmt"
	"strings"

	cerrors "github.com/angelmsger/agent-cli-kit/errors"
)

// NormalizeContextName trims and lower-cases a context name. Use it on every
// name about to be saved, never on a name being looked up: lookups ignore
// case already, and an existing mixed-case context keeps its spelling.
func NormalizeContextName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// NormalizeUsername trims a username and lower-cases it only when it is an
// email address. Other usernames may be case-sensitive on the server, so a
// CLI calls this only where its users sign in by email.
func NormalizeUsername(s string) string {
	s = strings.TrimSpace(s)
	if strings.ContainsRune(s, '@') {
		return strings.ToLower(s)
	}
	return s
}

// QuoteArg quotes s for a POSIX shell, so a step that names it can be pasted
// as written.
func QuoteArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// UnknownContextError reports that a context named in a command argument does
// not exist.
func UnknownContextError(name string, available []string) *cerrors.CLIError {
	return cerrors.Newf(cerrors.CategoryConfig, "UNKNOWN_CONTEXT", "context %q is not defined", name).
		WithHint(UnknownContextHint(name, available)).
		WithNextSteps(ListContextsStep())
}

// UseContext makes the named context current and returns its stored name. It
// returns UNKNOWN_CONTEXT when there is no such context.
func UseContext[C, D any](f *File[C, D], name string) (string, error) {
	c, ok := f.Context(name)
	if !ok {
		return "", UnknownContextError(name, f.ContextNames())
	}
	f.CurrentContext = nameOf(c)
	return f.CurrentContext, nil
}

// DeleteContext removes the named context and returns it. When it was
// current, the first remaining context becomes current. It returns
// UNKNOWN_CONTEXT when there is no such context and LAST_CONTEXT when it is
// the only one.
func DeleteContext[C, D any](f *File[C, D], name string) (C, error) {
	target, ok := f.Context(name)
	if !ok {
		var zero C
		return zero, UnknownContextError(name, f.ContextNames())
	}
	if len(f.Contexts) == 1 {
		var zero C
		return zero, cerrors.New(cerrors.CategoryUsage, "LAST_CONTEXT", "cannot delete the only context")
	}
	stored := nameOf(target)
	kept := f.Contexts[:0]
	for _, c := range f.Contexts {
		if nameOf(c) != stored {
			kept = append(kept, c)
		}
	}
	f.Contexts = kept
	if strings.EqualFold(f.CurrentContext, stored) {
		f.CurrentContext = nameOf(f.Contexts[0])
	}
	return target, nil
}

// SourceLabel renders a ContextSource* value as a phrase for the help block
// and the context notice.
func SourceLabel(source string) string {
	w := schema().Words
	switch source {
	case ContextSourceFlag:
		return "selected via " + w.ContextFlag
	case ContextSourceEnv:
		return "selected via " + w.ContextEnv
	case ContextSourceCurrent:
		return "selected via the saved current_context"
	case ContextSourceSingle:
		return "the only configured context"
	case ContextSourceDefault:
		return `selected as the "default" context`
	default:
		return "no context selected"
	}
}

// ReminderBlock is the text a CLI appends to --help when more than one
// context is configured, so a reader sees which context commands will use and
// how to pick another.
func ReminderBlock(active, source string, names []string) string {
	w := schema().Words
	var head string
	if active == "" {
		head = fmt.Sprintf("No active context selected (%d configured: %s).",
			len(names), strings.Join(names, ", "))
	} else {
		head = fmt.Sprintf("Active context: %s  (%d configured: %s; %s)",
			active, len(names), strings.Join(names, ", "), SourceLabel(source))
	}
	return head + "\n" +
		"Multiple contexts are configured — verify you are targeting the intended one.\n" +
		"Override per call with " + w.ContextFlag + " <name> or " + w.ContextEnv + "=<name>, or\n" +
		"change the default with `" + step(w.UseContext) + " <name>`.\n"
}

// ContextNotice is the _notice payload a CLI writes to stderr when a command
// runs against one of several contexts that was chosen implicitly.
func ContextNotice(active, source string, names []string) map[string]any {
	w := schema().Words
	target := active
	if target == "" {
		target = "the resolved context"
	}
	return map[string]any{"_notice": map[string]any{
		"context": map[string]any{
			"active":       active,
			"available":    names,
			"selected_via": source,
			"message": fmt.Sprintf("Multiple contexts are configured and this one was selected implicitly (%s). "+
				"Confirm %q is the intended target before trusting results.", SourceLabel(source), target),
			"override": w.ContextFlag + " <name> or " + w.ContextEnv + "=<name>",
			"silence":  "set " + w.NoHintEnv + "=1 to suppress",
		},
	}}
}
