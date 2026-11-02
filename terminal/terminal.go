// Package terminal decides whether a stream is an interactive terminal and
// asks a person questions on one.
//
// Everything takes explicit streams, so a command passes its own (Cobra's
// InOrStdin and ErrOrStderr) and a test can drive a prompt without replacing
// the process's stdin or stderr. A CLI decides when to prompt: it prompts only
// when Is(stdin) holds and otherwise returns a structured error, so an agent
// gets an actionable answer instead of a hang or a prompt nobody reads.
package terminal

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/term"
)

// Is reports whether stream is an interactive terminal: a file whose
// descriptor is a TTY. A pipe, a regular file, /dev/null and any in-memory
// reader or writer are not. Cron, CI and agent runners often start a command
// with stdin on /dev/null, which is a character device but not a terminal,
// so a file-mode check is not enough.
func Is(stream any) bool {
	f, ok := stream.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(f.Fd()))
}

// Prompter asks questions on Out and reads the answers from In. By convention
// Out is stderr, so stdout stays a clean data channel.
type Prompter struct {
	In  io.Reader
	Out io.Writer
}

// Every prompt shows its question before reading, and reads nothing when the
// question cannot be written: an answer to a question nobody saw is not one.

// Line writes "label: ", or "label [def]: " when def is set, and reads one
// line. The answer is trimmed, and an empty answer returns def. At the end of
// input it returns what was typed, or io.EOF when nothing was.
func (p Prompter) Line(label, def string) (string, error) {
	question := label + ": "
	if def != "" {
		question = label + " [" + def + "]: "
	}
	if _, err := io.WriteString(p.Out, question); err != nil {
		return "", err
	}
	line, err := ReadLine(p.In)
	if err != nil {
		return "", err
	}
	if line = strings.TrimSpace(line); line == "" {
		return def, nil
	}
	return line, nil
}

// Choice asks until the answer matches one of allowed, ignoring case, and
// returns the allowed spelling. An empty answer returns def.
func (p Prompter) Choice(label string, allowed []string, def string) (string, error) {
	for {
		in, err := p.Line(label, def)
		if err != nil {
			return "", err
		}
		for _, a := range allowed {
			if strings.EqualFold(in, a) {
				return a, nil
			}
		}
		if _, err := fmt.Fprintf(p.Out, "  please choose one of: %s\n", strings.Join(allowed, ", ")); err != nil {
			return "", err
		}
	}
}

// Confirm writes "question [y/N] " and reports whether the answer is y or yes,
// ignoring case. Anything else, including an empty answer or the end of
// input, is a no. A read failure is a no, returned with its error.
func (p Prompter) Confirm(question string) (bool, error) {
	if _, err := io.WriteString(p.Out, question+" [y/N] "); err != nil {
		return false, err
	}
	line, err := ReadLine(p.In)
	if errors.Is(err, io.EOF) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

// Secret writes "label: " and reads a value without echoing it when In is a
// terminal. Other input, such as a pipe, has no hidden read, so the value is
// read as a plain line and whatever feeds the pipe sees it. The answer is
// trimmed.
func (p Prompter) Secret(label string) (string, error) {
	if _, err := io.WriteString(p.Out, label+": "); err != nil {
		return "", err
	}
	if !Is(p.In) {
		line, err := ReadLine(p.In)
		return strings.TrimSpace(line), err
	}
	fd := int(p.In.(interface{ Fd() uintptr }).Fd())
	b, err := term.ReadPassword(fd)
	_, _ = io.WriteString(p.Out, "\n") // the hidden read left the cursor after the label
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// ReadLine reads one line from r a byte at a time, so it never consumes input
// past the newline: a later hidden read on the same terminal must find the
// next line intact. Carriage returns are dropped. The end of input completes
// a partial line, and returns io.EOF when nothing was read. Any other read
// failure returns its error and no line, so a truncated answer is never used.
func ReadLine(r io.Reader) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				return b.String(), nil
			}
			if buf[0] != '\r' {
				b.WriteByte(buf[0])
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) && b.Len() > 0 {
				return b.String(), nil
			}
			return "", err
		}
	}
}
