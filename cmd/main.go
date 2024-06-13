package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/a-h/imports"
)

func main() {
	dir := "/test.go"
	src := `package test

templ Hello(name string) {
				{ fmt.Sprintf("Hello, %s", name) }
}`
	if err := run(dir, src); err != nil {
		log.Fatalf("failed to run: %v", err)
	}
}

func run(dir, src string) error {
	tf, err := imports.Process(dir, src)
	if err != nil {
		return fmt.Errorf("failed to process file: %w", err)
	}
	actual := new(strings.Builder)
	if err := tf.Write(actual); err != nil {
		return fmt.Errorf("failed to write template file: %w", err)
	}
	if _, err = io.Copy(os.Stdout, strings.NewReader(actual.String())); err != nil {
		return fmt.Errorf("failed to write to stdout: %w", err)
	}
	return nil
}
