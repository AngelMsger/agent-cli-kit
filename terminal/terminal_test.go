package terminal

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsRejectsEveryNonTerminal covers the inputs agents and automation start
// commands with. /dev/null is the case a file-mode check gets wrong: it is a
// character device, like a terminal. A real terminal cannot be opened in CI.
func TestIsRejectsEveryNonTerminal(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	regular, err := os.Create(filepath.Join(t.TempDir(), "input"))
	if err != nil {
		t.Fatal(err)
	}
	defer regular.Close()
	var nilFile *os.File

	for name, stream := range map[string]any{
		"/dev/null":    devNull,
		"pipe reader":  r,
		"pipe writer":  w,
		"regular file": regular,
		"buffer":       &bytes.Buffer{},
		"nil file":     nilFile,
		"nil":          nil,
	} {
		if Is(stream) {
			t.Errorf("Is(%s) = true", name)
		}
	}
}

func TestLine(t *testing.T) {
	for _, tc := range []struct {
		input, def, want, prompt string
	}{
		{"  ada  \n", "", "ada", "Name: "},
		{"\n", "main", "main", "Branch [main]: "},
		{"dev\r\n", "main", "dev", "Branch [main]: "},
		{"no newline", "", "no newline", "Name: "},
	} {
		var out bytes.Buffer
		label := strings.TrimSuffix(strings.SplitN(tc.prompt, " [", 2)[0], ": ")
		got, err := Prompter{In: strings.NewReader(tc.input), Out: &out}.Line(label, tc.def)
		if err != nil || got != tc.want || out.String() != tc.prompt {
			t.Errorf("Line(%q) = %q, %v, prompt %q; want %q, prompt %q", tc.input, got, err, out.String(), tc.want, tc.prompt)
		}
	}
	if _, err := (Prompter{In: strings.NewReader(""), Out: io.Discard}).Line("Name", "x"); !errors.Is(err, io.EOF) {
		t.Fatalf("Line at end of input = %v, want io.EOF", err)
	}
}

func TestChoiceAsksUntilAnAllowedAnswer(t *testing.T) {
	var out bytes.Buffer
	p := Prompter{In: strings.NewReader("maybe\nEDIT\n"), Out: &out}
	got, err := p.Choice("Action", []string{"edit", "add"}, "")
	if err != nil || got != "edit" {
		t.Fatalf("Choice = %q, %v; want the allowed spelling edit", got, err)
	}
	const want = "Action:   please choose one of: edit, add\nAction: "
	if out.String() != want {
		t.Fatalf("prompts %q, want %q", out.String(), want)
	}
	got, err = Prompter{In: strings.NewReader("\n"), Out: io.Discard}.Choice("Action", []string{"edit", "add"}, "add")
	if err != nil || got != "add" {
		t.Fatalf("empty answer = %q, %v; want the default", got, err)
	}
}

func TestConfirm(t *testing.T) {
	for input, want := range map[string]bool{
		"y\n": true, "YES\n": true, " yes \n": true,
		"n\n": false, "\n": false, "sure\n": false, "": false,
	} {
		var out bytes.Buffer
		got, err := Prompter{In: strings.NewReader(input), Out: &out}.Confirm("Delete comment 42?")
		if err != nil || got != want {
			t.Errorf("Confirm(%q) = %v, %v; want %v", input, got, err, want)
		}
		if out.String() != "Delete comment 42? [y/N] " {
			t.Errorf("prompt %q", out.String())
		}
	}
	readErr := errors.New("read failed")
	if ok, err := (Prompter{In: failingReader{readErr}, Out: io.Discard}).Confirm("Delete?"); ok || !errors.Is(err, readErr) {
		t.Fatalf("Confirm on a read failure = %v, %v; want false, the error", ok, err)
	}
	// A "y" cut off by a read failure is not an answer.
	cut := io.MultiReader(strings.NewReader("y"), failingReader{readErr})
	if ok, err := (Prompter{In: cut, Out: io.Discard}).Confirm("Delete?"); ok || !errors.Is(err, readErr) {
		t.Fatalf("Confirm on a partial answer then a read failure = %v, %v; want false, the error", ok, err)
	}
}

// When the question cannot be shown, nothing is read: an answer to an
// invisible question is not consent.
func TestPromptsStopWhenTheQuestionCannotBeShown(t *testing.T) {
	writeErr := errors.New("stderr closed")
	in := strings.NewReader("y\n")
	p := Prompter{In: in, Out: failingWriter{writeErr}}
	if ok, err := p.Confirm("Delete?"); ok || !errors.Is(err, writeErr) {
		t.Fatalf("Confirm = %v, %v; want false, the write error", ok, err)
	}
	if _, err := p.Line("Name", ""); !errors.Is(err, writeErr) {
		t.Fatalf("Line = %v, want the write error", err)
	}
	if _, err := p.Secret("Token"); !errors.Is(err, writeErr) {
		t.Fatalf("Secret = %v, want the write error", err)
	}
	if in.Len() != len("y\n") {
		t.Fatal("a prompt read input after failing to show its question")
	}
}

// A secret from a pipe has no hidden read, so it arrives as a plain line.
func TestSecretFromAPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := w.WriteString(" s3cret \nnext\n"); err != nil {
		t.Fatal(err)
	}
	w.Close()
	var out bytes.Buffer
	p := Prompter{In: r, Out: &out}
	got, err := p.Secret("API token")
	if err != nil || got != "s3cret" || out.String() != "API token: " {
		t.Fatalf("Secret = %q, %v, prompt %q", got, err, out.String())
	}
	if next, _ := p.Line("Next", ""); next != "next" {
		t.Fatalf("the following line = %q; Secret read past its own line", next)
	}
}

// Only the end of input completes an unterminated line; any other read failure
// is returned, so a truncated answer or secret is never used.
func TestReadLineReturnsReadFailures(t *testing.T) {
	readErr := errors.New("read failed")
	if line, err := ReadLine(io.MultiReader(strings.NewReader("s3cr"), failingReader{readErr})); !errors.Is(err, readErr) || line != "" {
		t.Fatalf("ReadLine = %q, %v; want no line and the error", line, err)
	}
}

// ReadLine must leave the rest of the input for the next reader, which is why
// it reads a byte at a time instead of buffering.
func TestReadLineStopsAtTheNewline(t *testing.T) {
	r := strings.NewReader("first\r\nsecond\nthird")
	for _, want := range []string{"first", "second", "third"} {
		got, err := ReadLine(r)
		if err != nil || got != want {
			t.Fatalf("ReadLine = %q, %v; want %q", got, err, want)
		}
	}
	if _, err := ReadLine(r); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadLine after the input = %v, want io.EOF", err)
	}
}

type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }

type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }
