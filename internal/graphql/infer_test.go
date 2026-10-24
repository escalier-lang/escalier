package graphql

import (
	"fmt"
	"os"
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"
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
	snaps.MatchSnapshot(t, soltype.Print(result.ResultType))
	snaps.MatchSnapshot(t, soltype.Print(result.VariablesType))
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
	snaps.MatchSnapshot(t, soltype.Print(result.ResultType))
	snaps.MatchSnapshot(t, soltype.Print(result.VariablesType))
}

func TestInferGraphQLQuery_SearchContentWithUnion(t *testing.T) {
	schema := loadSchema(t)

	queryStr := `
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

	queryDoc := gqlparser.MustLoadQueryWithRules(schema, queryStr, rules.NewDefaultRules())
	result := InferGraphQLQuery(schema, queryDoc)

	if result.ResultType == nil {
		t.Error("Expected ResultType to be non-nil")
	}
	if result.VariablesType == nil {
		t.Error("Expected VariablesType to be non-nil")
	}
	// Snapshot test the inferred types
	snaps.MatchSnapshot(t, soltype.Print(result.ResultType))
	snaps.MatchSnapshot(t, soltype.Print(result.VariablesType))
}

func TestInferGraphQLQuery_ScalarTypes(t *testing.T) {
	schema := loadSchema(t)

	queryStr := `
		query GetNodeWithScalars($nodeId: ID!) {
			node(id: $nodeId) {
				id
				createdAt
				... on Post {
					metadata
				}
			}
		}
	`

	queryDoc := gqlparser.MustLoadQueryWithRules(schema, queryStr, rules.NewDefaultRules())
	result := InferGraphQLQuery(schema, queryDoc)

	if result.ResultType == nil {
		t.Error("Expected ResultType to be non-nil")
	}
	if result.VariablesType == nil {
		t.Error("Expected VariablesType to be non-nil")
	}

	snaps.MatchSnapshot(t, soltype.Print(result.ResultType))
	snaps.MatchSnapshot(t, soltype.Print(result.VariablesType))
}

func TestInferGraphQLQuery_ListTypes(t *testing.T) {
	schema := loadSchema(t)

	queryStr := `
		query GetPostsAndComments($offset: Int = 0, $limit: Int = 10, $commentLimit: Int = 5) {
			listPosts(offset: $offset, limit: $limit) {
				id
				title
				content
				comments(limit: $commentLimit) {
					id
					message
				}
			}
		}
	`

	queryDoc := gqlparser.MustLoadQueryWithRules(schema, queryStr, rules.NewDefaultRules())
	result := InferGraphQLQuery(schema, queryDoc)

	if result.ResultType == nil {
		t.Error("Expected ResultType to be non-nil")
	}
	if result.VariablesType == nil {
		t.Error("Expected VariablesType to be non-nil")
	}

	snaps.MatchSnapshot(t, soltype.Print(result.ResultType))
	snaps.MatchSnapshot(t, soltype.Print(result.VariablesType))
}

func TestInferGraphQLQuery_InputObjectTypes(t *testing.T) {
	schema := loadSchema(t)

	mutationStr := `
		mutation CreatePostWithMetadata($postData: PostInput!) {
			createPost(input: $postData) {
				id
				title
				content
				metadata
				author {
					id
					name
				}
			}
		}
	`

	queryDoc := gqlparser.MustLoadQueryWithRules(schema, mutationStr, rules.NewDefaultRules())
	result := InferGraphQLQuery(schema, queryDoc)

	if result.ResultType == nil {
		t.Error("Expected ResultType to be non-nil")
	}
	if result.VariablesType == nil {
		t.Error("Expected VariablesType to be non-nil")
	}

	snaps.MatchSnapshot(t, soltype.Print(result.ResultType))
	snaps.MatchSnapshot(t, soltype.Print(result.VariablesType))
}

func TestInferGraphQLQuery_SubscriptionTypes(t *testing.T) {
	schema := loadSchema(t)

	subscriptionStr := `
		subscription WatchPostCreated {
			postCreated {
				id
				title
				author {
					id
					name
					role
				}
			}
		}
	`

	queryDoc := gqlparser.MustLoadQueryWithRules(schema, subscriptionStr, rules.NewDefaultRules())
	result := InferGraphQLQuery(schema, queryDoc)

	if result.ResultType == nil {
		t.Error("Expected ResultType to be non-nil")
	}
	if result.VariablesType == nil {
		t.Error("Expected VariablesType to be non-nil")
	}

	snaps.MatchSnapshot(t, soltype.Print(result.ResultType))
	snaps.MatchSnapshot(t, soltype.Print(result.VariablesType))
}

func TestInferGraphQLQuery_InterfaceTypes(t *testing.T) {
	schema := loadSchema(t)

	queryStr := `
		query GetNodeInterface($nodeId: ID!) {
			node(id: $nodeId) {
				id
				createdAt
				... on User {
					name
					role
					contactInfo {
						email
						phone
					}
				}
				... on Post {
					title
					content
					author {
						name
					}
				}
				... on Comment {
					message
					author {
						name
					}
					post {
						title
					}
				}
			}
		}
	`

	queryDoc := gqlparser.MustLoadQueryWithRules(schema, queryStr, rules.NewDefaultRules())
	result := InferGraphQLQuery(schema, queryDoc)

	if result.ResultType == nil {
		t.Error("Expected ResultType to be non-nil")
	}
	if result.VariablesType == nil {
		t.Error("Expected VariablesType to be non-nil")
	}

	snaps.MatchSnapshot(t, soltype.Print(result.ResultType))
	snaps.MatchSnapshot(t, soltype.Print(result.VariablesType))
}

func TestInferGraphQLQuery_NullableFields(t *testing.T) {
	schema := loadSchema(t)

	queryStr := `
		query GetPostWithNullableFields($postId: ID!) {
			getUser(id: $postId) {
				id
				name
				contactInfo {
					email
					phone
				}
				posts {
					id
					title
					content
					metadata
				}
			}
		}
	`

	queryDoc := gqlparser.MustLoadQueryWithRules(schema, queryStr, rules.NewDefaultRules())
	result := InferGraphQLQuery(schema, queryDoc)

	if result.ResultType == nil {
		t.Error("Expected ResultType to be non-nil")
	}
	if result.VariablesType == nil {
		t.Error("Expected VariablesType to be non-nil")
	}

	snaps.MatchSnapshot(t, soltype.Print(result.ResultType))
	snaps.MatchSnapshot(t, soltype.Print(result.VariablesType))
}

func TestInferGraphQLQuery_ComplexMutation(t *testing.T) {
	schema := loadSchema(t)

	mutationStr := `
		mutation UpdateUserAndCreatePost($userId: ID!, $newRole: Role!, $postInput: PostInput!) {
			updateUserRole(userId: $userId, role: $newRole) {
				id
				name
				role
				contactInfo {
					email
					phone
				}
			}
			createPost(input: $postInput) {
				id
				title
				content
				metadata
				author {
					id
					name
					role
				}
			}
		}
	`

	queryDoc := gqlparser.MustLoadQueryWithRules(schema, mutationStr, rules.NewDefaultRules())
	result := InferGraphQLQuery(schema, queryDoc)

	if result.ResultType == nil {
		t.Error("Expected ResultType to be non-nil")
	}
	if result.VariablesType == nil {
		t.Error("Expected VariablesType to be non-nil")
	}

	snaps.MatchSnapshot(t, soltype.Print(result.ResultType))
	snaps.MatchSnapshot(t, soltype.Print(result.VariablesType))
}

func TestInferGraphQLQuery_SubscriptionWithVariables(t *testing.T) {
	schema := loadSchema(t)

	subscriptionStr := `
		subscription WatchComments($postId: ID!) {
			commentAdded(postId: $postId) {
				id
				message
				author {
					id
					name
					role
				}
				post {
					id
					title
				}
			}
		}
	`

	queryDoc := gqlparser.MustLoadQueryWithRules(schema, subscriptionStr, rules.NewDefaultRules())
	result := InferGraphQLQuery(schema, queryDoc)

	if result.ResultType == nil {
		t.Error("Expected ResultType to be non-nil")
	}
	if result.VariablesType == nil {
		t.Error("Expected VariablesType to be non-nil")
	}

	snaps.MatchSnapshot(t, soltype.Print(result.ResultType))
	snaps.MatchSnapshot(t, soltype.Print(result.VariablesType))
}

// TestInferGraphQLQuery_BooleanAndNullableListFields asserts that a `Boolean` field infers
// `boolean`, and that a nullable list of nullable elements infers an optional `Array` that
// may also be `null`.
func TestInferGraphQLQuery_BooleanAndNullableListFields(t *testing.T) {
	schema := loadSchema(t)

	queryStr := `
		query GetUserFlags($userId: ID!) {
			getUser(id: $userId) {
				verified
				tags
			}
		}
	`

	queryDoc := gqlparser.MustLoadQueryWithRules(schema, queryStr, rules.NewDefaultRules())
	result := InferGraphQLQuery(schema, queryDoc)

	require.NotNil(t, result.ResultType)
	snaps.MatchInlineSnapshot(t, soltype.Print(result.ResultType), snaps.Inline("{getUser?: {verified: boolean, tags?: Array<string | null> | null} | null}"))
}

// TestInferGraphQLQueryRecordsProvenance covers the schema and query positions the inference
// records in its Prov table for the types it mints.
func TestInferGraphQLQueryRecordsProvenance(t *testing.T) {
	schema := loadSchema(t)

	queryStr := `query GetUserFlags($userId: ID!) {
	getUser(id: $userId) {
		role
		tags
	}
}`

	queryDoc, err := parser.ParseQuery(&ast.Source{Name: "query.graphql", Input: queryStr})
	require.NoError(t, err)
	require.Empty(t, validator.ValidateWithRules(schema, queryDoc, rules.NewDefaultRules()))
	result := InferGraphQLQuery(schema, queryDoc)

	getUser := propType(t, result.ResultType, "getUser")
	user := getUser.(*soltype.UnionType).Types[0]
	tests := []struct {
		name string
		typ  soltype.Type
		want string
	}{
		{name: "the result object reads the Query type", typ: result.ResultType, want: "schema.graphql:77"},
		{name: "the variables object reads the operation", typ: result.VariablesType, want: "query.graphql:1"},
		{name: "a nullable field reads its field definition", typ: getUser, want: "schema.graphql:80"},
		{name: "an enum reads its type definition", typ: propType(t, user, "role"), want: "schema.graphql:12"},
		{name: "a list reads its field selection", typ: listOf(t, propType(t, user, "tags")), want: "query.graphql:4"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			origin, ok := result.Prov[test.typ].(solver.FromExternal)
			require.True(t, ok)
			gql, ok := origin.Source.(*GraphQLProvenance)
			require.True(t, ok)
			require.Equal(t, test.want, fmt.Sprintf("%s:%d", gql.Position.Src.Name, gql.Position.Line))
		})
	}
}

// propType returns the type of the property name on the object t.
func propType(t *testing.T, typ soltype.Type, name string) soltype.Type {
	t.Helper()
	obj, ok := typ.(*soltype.ObjectType)
	require.True(t, ok)
	prop, ok := obj.Prop(name)
	require.True(t, ok)
	return prop.Type
}

// listOf returns the Array member of a nullable list type `Array<T> | null`.
func listOf(t *testing.T, typ soltype.Type) soltype.Type {
	t.Helper()
	union, ok := typ.(*soltype.UnionType)
	require.True(t, ok)
	return union.Types[0]
}
