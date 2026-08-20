package sourcecontrol

import (
	"context"
	"testing"

	"github.com/kandev/kandev/pkg/pluginsdk"
	"github.com/stretchr/testify/require"
)

type referenceServiceStub struct {
	candidates     []pluginsdk.EntityReferenceCandidate
	allowed        bool
	searchCalls    int
	authorizeCalls int
	gotWorkspace   string
	gotPurpose     string
	gotReference   map[string]any
}

func (s *referenceServiceStub) Search(_ context.Context, workspaceID, query string, limit int) ([]pluginsdk.EntityReferenceCandidate, error) {
	s.searchCalls++
	s.gotWorkspace = workspaceID
	return s.candidates, nil
}

func (s *referenceServiceStub) Authorize(_ context.Context, workspaceID, purpose string, reference map[string]any) (bool, error) {
	s.authorizeCalls++
	s.gotWorkspace = workspaceID
	s.gotPurpose = purpose
	s.gotReference = reference
	return s.allowed, nil
}

func TestSubmissionReauthorizesAfterSearchResultIsRevoked(t *testing.T) {
	references := &referenceServiceStub{
		allowed: true,
		candidates: []pluginsdk.EntityReferenceCandidate{{
			ProviderLocalID: "connection-7/repository-9/42",
			Title:           "Provider-neutral hooks",
			URL:             "https://code.example/tools/widgets/changes/42",
			Attributes: map[string]any{
				"connection_scope": "connection-7",
				"repository_id":    "immutable-repository-9",
				"number":           float64(42),
			},
		}},
	}
	extension := &Extension{
		ReferenceSource: "acme",
		References:      references,
	}

	search, err := extension.SearchEntityReferences(context.Background(), &pluginsdk.SearchEntityReferencesRequest{
		Source: "acme", WorkspaceID: "workspace-1", Query: "hooks", Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, search.Candidates, 1)
	require.Equal(t, 1, references.searchCalls)

	// Access changes after selection. Submission must ask the provider again;
	// the successful search result cannot authorize the final message.
	references.allowed = false
	reference := map[string]any{
		"id":               search.Candidates[0].ProviderLocalID,
		"connection_scope": "connection-7",
		"repository_id":    "immutable-repository-9",
		"number":           float64(42),
	}
	authorized, err := extension.AuthorizeEntityReference(context.Background(), &pluginsdk.AuthorizeEntityReferenceRequest{
		Source: "acme", WorkspaceID: "workspace-1", Purpose: "submission", Reference: reference,
	})
	require.NoError(t, err)
	require.False(t, authorized.Allowed)
	require.Equal(t, 1, references.authorizeCalls)
	require.Equal(t, "workspace-1", references.gotWorkspace)
	require.Equal(t, "submission", references.gotPurpose)
	require.Equal(t, reference, references.gotReference)
}
