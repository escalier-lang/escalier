package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestWorkspaceRootURI(t *testing.T) {
	rootURI := protocol.DocumentUri("file:///root")
	tests := []struct {
		name     string
		params   protocol.InitializeParams
		expected string
	}{
		{
			name: "first workspace folder",
			params: protocol.InitializeParams{
				WorkspaceFolders: []protocol.WorkspaceFolder{
					{URI: "file:///first", Name: "first"},
					{URI: "file:///second", Name: "second"},
				},
				RootURI: &rootURI,
			},
			expected: "file:///first",
		},
		{
			name:     "rootUri when there are no workspace folders",
			params:   protocol.InitializeParams{RootURI: &rootURI},
			expected: "file:///root",
		},
		{
			name:     "empty when the client names no root",
			params:   protocol.InitializeParams{},
			expected: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.expected, workspaceRootURI(&test.params))
		})
	}
}
