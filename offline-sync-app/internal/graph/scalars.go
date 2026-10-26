package graph

import (
	"github.com/99designs/gqlgen/graphql"
	"github.com/google/uuid"
)

// Bound to the GraphQL ID type in gqlgen.yml so a malformed id is a user error, not an "internal error".
type ID = uuid.UUID

func MarshalID(id ID) graphql.Marshaler { return graphql.MarshalString(id.String()) }

func UnmarshalID(v any) (ID, error) {
	s, ok := v.(string)
	if !ok {
		return uuid.Nil, badInput("id must be a string")
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, badInput("id is not a valid UUID")
	}
	return id, nil
}
