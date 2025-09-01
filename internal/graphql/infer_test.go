package graphql

import (
	"os"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/validator/rules"
)

func loadSchema(t *testing.T) *ast.Schema {
	// Read schema.graphql from disk
	schemaBytes, err := os.ReadFile("schema.graphql")
	if err != nil {
		t.Fatalf("failed to read schema.graphql: %v", err)
	}
	schemaStr := string(schemaBytes)

	// Convert SchemaDocument into a usable schema.
	schema := gqlparser.MustLoadSchema(&ast.Source{
		Name:    "schema.graphql",
		Input:   schemaStr,
		BuiltIn: false,
	})

	return schema
}

func TestInferGraphQLQuery_GetUserAndPosts(t *testing.T) {
	schema := loadSchema(t)

	// Define a sample GraphQL query string.
	queryStr := `
		query GetUserAndPosts($userId: ID!, $postLimit: Int = 5) {
		getUser(id: $userId) {
			id
			name
			role
			contactInfo {
				email
				phone
			}
			posts {
				id
				title
				content
				comments(limit: $postLimit) {
					id
					message
					author {
						id
						name
						}
					}
				}
			}
		}
	`

	// Parse the query against the schema.
	queryDoc := gqlparser.MustLoadQueryWithRules(schema, queryStr, rules.NewDefaultRules())

	result := InferGraphQLQuery(schema, queryDoc)

	// Verify that result types are not nil
	if result.ResultType == nil {
		t.Error("Expected ResultType to be non-nil")
	}
	if result.VariablesType == nil {
		t.Error("Expected VariablesType to be non-nil")
	}

	// Snapshot test the inferred types
	snaps.MatchSnapshot(t, result.ResultType.String())
	snaps.MatchSnapshot(t, result.VariablesType.String())
}

func TestInferGraphQLQuery_CreatePostAndComment(t *testing.T) {
	schema := loadSchema(t)

	mutationStr := `
		mutation CreatePostAndComment {
			createPost(input: {
				title: "Understanding GraphQL Unions",
				content: "Unions let you return different object types from a field.",
				metadata: {
				tags: ["GraphQL", "Schema", "Unions"],
				estimatedReadTime: 5
				}
			}) {
				id
				title
				author {
					id
					name
				}
			}

			addComment(input: {
				postId: "post-456",
				message: "Great explanation of unions!"
			}) {
				id
				message
				author {
					id
					name
				}
				post {
					id
					title
				}
			}
		}
	`

	// Parse the query against the schema.
	queryDoc := gqlparser.MustLoadQueryWithRules(schema, mutationStr, rules.NewDefaultRules())

	result := InferGraphQLQuery(schema, queryDoc)

	// Verify that result types are not nil
	if result.ResultType == nil {
		t.Error("Expected ResultType to be non-nil")
	}
	if result.VariablesType == nil {
		t.Error("Expected VariablesType to be non-nil")
	}

	// Snapshot test the inferred types
	snaps.MatchSnapshot(t, result.ResultType.String())
	snaps.MatchSnapshot(t, result.VariablesType.String())
}

func TestInferGraphQLQuery_SearchContentWithUnion(t *testing.T) {
	schema := loadSchema(t)

	queryWithUnionStr := `
	query SearchContent($searchTerm: String!) {
		search(text: $searchTerm) {
			... on User {
				id
				name
				role
				contactInfo {
					email
				}
			}

			... on Post {
				id
				title
				content
				author {
					name
					role
				}
			}

			... on Comment {
				id
				message
				author {
					name
				}
				post {
					title
				}
			}
		}
	}`

	// Parse the query against the schema.
	queryDoc := gqlparser.MustLoadQueryWithRules(schema, queryWithUnionStr, rules.NewDefaultRules())

	result := InferGraphQLQuery(schema, queryDoc)

	// Verify that result types are not nil
	if result.ResultType == nil {
		t.Error("Expected ResultType to be non-nil")
	}
	if result.VariablesType == nil {
		t.Error("Expected VariablesType to be non-nil")
	}

	// Snapshot test the inferred types
	snaps.MatchSnapshot(t, result.ResultType.String())
	snaps.MatchSnapshot(t, result.VariablesType.String())
}
