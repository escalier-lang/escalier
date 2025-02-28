package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/TobiasYin/go-lsp/logs"
	"github.com/TobiasYin/go-lsp/lsp"
	"github.com/TobiasYin/go-lsp/lsp/defines"
)

var logPath *string

func init() {
	var logger *log.Logger
	defer func() {
		logs.Init(logger)
	}()
	logPath = flag.String("logs", "", "logs file path")
	if logPath == nil || *logPath == "" {
		logger = log.New(os.Stderr, "", 0)
		return
	}
	p := *logPath
	f, err := os.Open(p)
	if err == nil {
		logger = log.New(f, "", 0)
		return
	}
	f, err = os.Create(p)
	if err == nil {
		logger = log.New(f, "", 0)
		return
	}
	panic(fmt.Sprintf("logs init error: %v", *logPath))
}

func main() {
	fmt.Fprintf(os.Stderr, "Hello, from lsp-server\n")

	// Create a new server
	server := lsp.NewServer(&lsp.Options{})

	server.OnDidChangeTextDocument(func(
		ctx context.Context,
		req *defines.DidChangeTextDocumentParams) (err error) {

		uri := req.TextDocument.Uri
		fmt.Fprintf(os.Stderr, "file changed: %s\n", uri)
		fmt.Fprintf(os.Stderr, "changes: %v\n", req.ContentChanges)

		// TODO:
		// - parse files when they change
		// - publish diagnostics if there are any errors

		return nil
	})

	server.Run()
}
