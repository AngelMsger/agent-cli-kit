package config

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/joho/godotenv"

	cerrors "github.com/angelmsger/agent-cli-kit/errors"
)

// DefaultContextName is the name of an unnamed context, and the context
// chosen when several exist and none is current.
const DefaultContextName = "default"

// Context selection sources, reported in Resolved.ContextSource. The first
// two are explicit (the caller named a context for this invocation); the rest
// are implicit fallbacks.
const (
	ContextSourceFlag    = "flag"            // the context flag
	ContextSourceEnv     = "env"             // the context variable
	ContextSourceCurrent = "current_context" // the file's current_context
	ContextSourceSingle  = "single"          // the only context
	ContextSourceDefault = "default-name"    // a context named "default"
	ContextSourceNone    = "none"            // nothing selected
)

// LoadOptions controls one Load.
type LoadOptions struct {
	// Setup resolves Context even when it does not exist yet, skips context
	// selection, and keeps only Service fields from the environment and .env,
	// before a secret can imply a scheme.
	Setup bool
	// DotenvPath is the .env file; "" means ".env". A missing file is empty.
	DotenvPath string
	// Flags is the highest layer, usually Encode of the CLI's flag struct.
	Flags Values
	// Context is the value of the context flag. It wins over the context
	// variable and the file's current_context.
	Context string
}

// Resolved is the outcome of Load.
type Resolved struct {
	// Values holds the merged settings, secrets included; Decode them into
	// the CLI's structs.
	Values Values
	// Sources maps each key in Values to the layer that supplied it:
	// "flag", "env", "dotenv", "file" or "default".
	Sources map[string]string
	// ActiveContext is the stored name of the context whose fields apply, or
	// "" when none does.
	ActiveContext string
	// ContextSource is the ContextSource* rule that chose ActiveContext.
	ContextSource string
	// ContextNames lists every context in the file, in file order.
	ContextNames []string
	// StoredServer is the server as the active context stores it, when that
	// spelling differs from, but is equivalent to, the server in effect. A
	// secret stored under the old spelling is looked up with it.
	StoredServer string
}

// Load merges the built-in defaults, the selected context and defaults of
// file, the .env file, the environment and opt.Flags, lowest first.
func Load[C, D any](file File[C, D], opt LoadOptions) (*Resolved, error) {
	ctxName, ctxSource := opt.Context, ContextSourceFlag
	if opt.Setup {
		if c, ok := file.Context(ctxName); ok {
			ctxName = nameOf(c)
		}
	} else {
		var err error
		ctxName, ctxSource, err = selectContext(file, opt.Context, os.Getenv(schema().Words.ContextEnv))
		if err != nil {
			return nil, err
		}
	}
	fileValues := fileLayer(file, ctxName)

	dotenvPath := opt.DotenvPath
	if dotenvPath == "" {
		dotenvPath = ".env"
	}
	dotValues, err := dotenvLayer(dotenvPath, opt.Setup)
	if err != nil {
		return nil, err
	}

	layers := []struct {
		name   string
		values Values
	}{
		{"default", DefaultValues()},
		{"file", fileValues},
		{"dotenv", dotValues},
		{"env", envLayer(opt.Setup)},
		{"flag", opt.Flags},
	}
	merged := Values{}
	sources := map[string]string{}
	for _, l := range layers {
		for k, v := range l.values {
			merged[k] = v
			sources[k] = l.name
		}
	}

	for _, f := range schema().Fields {
		if slices.Contains(f.ResetBy, KeyServer) && !sameServer(merged[KeyServer], fileValues[KeyServer]) {
			delete(merged, f.Key)
			delete(sources, f.Key)
		}
	}
	if hook := schema().AfterMerge; hook != nil {
		hook(merged, sources)
	}
	return &Resolved{
		Values:        merged,
		Sources:       sources,
		ActiveContext: ctxName,
		ContextSource: ctxSource,
		ContextNames:  file.ContextNames(),
		StoredServer:  storedServer(file, ctxName, merged[KeyServer]),
	}, nil
}

// StoredContext resolves a context from its stored fields, the file defaults
// and the built-in defaults only, so nothing from the environment or .env of
// the current invocation leaks in. AfterMerge runs as in Load.
func StoredContext[C, D any](c C, defaults D) Values {
	values := DefaultValues()
	sources := make(map[string]string, len(values))
	for key := range values {
		sources[key] = "default"
	}
	for key, value := range fileLayer(File[C, D]{Contexts: []C{c}, Defaults: defaults}, nameOf(c)) {
		values[key] = value
		sources[key] = "file"
	}
	if hook := schema().AfterMerge; hook != nil {
		hook(values, sources)
	}
	return values
}

// ExplainField returns the layer that supplied key, or "default".
func ExplainField(sources map[string]string, key string) string {
	if s, ok := sources[key]; ok {
		return s
	}
	return "default"
}

// selectContext picks the active context: the flag, the context variable, the
// file's current_context, the only context, then a context named "default".
// It returns "" and ContextSourceNone when none applies, and UNKNOWN_CONTEXT
// when a named context does not exist.
func selectContext[C, D any](f File[C, D], flagCtx, envCtx string) (string, string, error) {
	w := schema().Words
	pick := func(name, from, source string) (string, string, error) {
		if c, ok := f.Context(name); ok {
			return nameOf(c), source, nil
		}
		return "", "", cerrors.Newf(cerrors.CategoryConfig, "UNKNOWN_CONTEXT",
			"context %q (from %s) is not defined in the config file", name, from).
			WithHint(UnknownContextHint(name, f.ContextNames())).
			WithNextSteps(ListContextsStep())
	}
	switch {
	case flagCtx != "":
		return pick(flagCtx, w.ContextFlag, ContextSourceFlag)
	case envCtx != "":
		return pick(envCtx, w.ContextEnv, ContextSourceEnv)
	case f.CurrentContext != "":
		return pick(f.CurrentContext, "current_context", ContextSourceCurrent)
	case len(f.Contexts) == 1:
		return nameOf(f.Contexts[0]), ContextSourceSingle, nil
	default:
		if _, ok := f.Context(DefaultContextName); ok {
			return DefaultContextName, ContextSourceDefault, nil
		}
		return "", ContextSourceNone, nil
	}
}

// UnknownContextHint is the hint for a context name that does not exist: the
// available names, else the command that lists them. Lookups ignore case, so
// no other spelling of name can be among them.
func UnknownContextHint(name string, available []string) string {
	if len(available) > 0 {
		return fmt.Sprintf("Available contexts: %s.", strings.Join(available, ", "))
	}
	return "Run `" + ListContextsStep() + "` to list defined contexts."
}

// fileLayer flattens the named context and the file defaults. An empty name
// yields the defaults alone.
func fileLayer[C, D any](f File[C, D], name string) Values {
	m := Values{}
	if name != "" {
		if c, ok := f.Context(name); ok {
			for k, v := range Encode(&c) {
				if k != KeyName {
					m[k] = v
				}
			}
		}
	}
	for k, v := range Encode(&f.Defaults) {
		m[k] = v
	}
	return m
}

// sameServer reports whether two spellings name one service. Unparsable
// spellings match only themselves.
func sameServer(a, b string) bool {
	if a == b {
		return true
	}
	left, e1 := NormalizeServiceURL(a)
	right, e2 := NormalizeServiceURL(b)
	return e1 == nil && e2 == nil && left == right
}

func storedServer[C, D any](f File[C, D], name, server string) string {
	c, ok := f.Context(name)
	if !ok {
		return ""
	}
	if stored := get(structValue(&c), KeyServer); stored != server && sameServer(stored, server) {
		return stored
	}
	return ""
}

// DefaultValues returns the built-in default of every field that has one, the
// lowest layer of Load. A setup wizard starts a new file from it.
func DefaultValues() Values {
	m := Values{}
	for _, f := range schema().Fields {
		if f.Default != "" {
			m[f.Key] = f.Default
		}
	}
	return m
}

// layerFromVars maps variable values onto keys. Empty values are skipped so
// they do not shadow lower layers. With setup, only Service fields are kept.
// A secret then implies its scheme, unless the layer sets the scheme itself.
func layerFromVars(vars map[string]string, setup bool) Values {
	fields := schema().Fields
	m := Values{}
	for _, f := range fields {
		if setup && !f.Service {
			continue
		}
		for _, name := range f.Env {
			if v := vars[name]; v != "" {
				m[f.Key] = v
				break
			}
		}
	}
	explicit, hasExplicit := m[KeyAuthScheme]
	for _, f := range fields {
		if _, ok := m[f.Key]; ok && f.Implies != "" {
			m[KeyAuthScheme] = f.Implies
			break
		}
	}
	if hasExplicit {
		m[KeyAuthScheme] = explicit
	}
	return m
}

func envLayer(setup bool) Values {
	vars := map[string]string{}
	for _, f := range schema().Fields {
		for _, name := range f.Env {
			if v, ok := os.LookupEnv(name); ok {
				vars[name] = v
			}
		}
	}
	return layerFromVars(vars, setup)
}

// dotenvLayer reads path without changing the process environment.
func dotenvLayer(path string, setup bool) (Values, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return Values{}, nil
		}
		return nil, err
	}
	vars, err := godotenv.Read(path)
	if err != nil {
		return nil, err
	}
	return layerFromVars(vars, setup), nil
}
