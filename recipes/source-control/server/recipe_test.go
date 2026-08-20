package sourcecontrol

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/kandev/kandev/pkg/pluginsdk"
	"github.com/stretchr/testify/require"
)

type attachedRepositoryStub struct {
	got        pluginsdk.VerifiedActionContext
	repository Repository
}

func (s *attachedRepositoryStub) ResolveAttached(_ context.Context, action pluginsdk.VerifiedActionContext) (Repository, error) {
	s.got = action
	return s.repository, nil
}

type changeRequestStub struct {
	gotRepository Repository
	gotSource     string
	gotInput      CreateChangeRequestInput
	created       ChangeRequest
	gotWorkspace  string
	gotReference  string
	resolved      ChangeRequest
}

func (s *changeRequestStub) ResolveReference(_ context.Context, workspaceID, reference string) (ChangeRequest, error) {
	s.gotWorkspace = workspaceID
	s.gotReference = reference
	return s.resolved, nil
}

func (s *changeRequestStub) Create(_ context.Context, repository Repository, source string, input CreateChangeRequestInput) (ChangeRequest, error) {
	s.gotRepository = repository
	s.gotSource = source
	s.gotInput = input
	return s.created, nil
}

type associationStub struct {
	linkErr      error
	linked       ChangeRequestIdentity
	taskID       string
	unlinked     ChangeRequestIdentity
	unlinkTaskID string
}

func (s *associationStub) Link(_ context.Context, taskID string, identity ChangeRequestIdentity) error {
	s.taskID = taskID
	s.linked = identity
	return s.linkErr
}

func (s *associationStub) Unlink(_ context.Context, taskID string, identity ChangeRequestIdentity) error {
	s.unlinkTaskID = taskID
	s.unlinked = identity
	return nil
}

type repositoryListStub struct {
	scope        string
	calls        int
	gotWorkspace string
	gotQuery     string
	gotCursor    RepositoryCursor
	gotLimit     int
	page         RepositoryPage
}

type repositoryDetailsStub struct {
	gotWorkspace  string
	gotURL        string
	inspected     *Repository
	gotIdentity   RepositoryIdentity
	resolved      Repository
	gotBranchRepo Repository
	branches      []Branch
}

type reviewReaderStub struct {
	gotWorkspace string
	gotTask      string
	reviews      []ReviewSummary
	associations []ReviewAssociation
}

func (s *reviewReaderStub) ForTask(_ context.Context, workspaceID, taskID string) ([]ReviewSummary, error) {
	s.gotWorkspace = workspaceID
	s.gotTask = taskID
	return s.reviews, nil
}

func (s *reviewReaderStub) Associations(_ context.Context, workspaceID string) ([]ReviewAssociation, error) {
	s.gotWorkspace = workspaceID
	return s.associations, nil
}

func (s *repositoryDetailsStub) Inspect(_ context.Context, workspaceID, rawURL string) (*Repository, error) {
	s.gotWorkspace = workspaceID
	s.gotURL = rawURL
	return s.inspected, nil
}

func (s *repositoryDetailsStub) Resolve(_ context.Context, workspaceID string, identity RepositoryIdentity) (Repository, error) {
	s.gotWorkspace = workspaceID
	s.gotIdentity = identity
	return s.resolved, nil
}

func (s *repositoryDetailsStub) ListBranches(_ context.Context, workspaceID string, repository Repository) ([]Branch, error) {
	s.gotWorkspace = workspaceID
	s.gotBranchRepo = repository
	return s.branches, nil
}

func (s *repositoryListStub) ConnectionScope(context.Context, string) (string, error) {
	return s.scope, nil
}

func (s *repositoryListStub) List(_ context.Context, workspaceID, query string, cursor RepositoryCursor, limit int) (RepositoryPage, error) {
	s.calls++
	s.gotWorkspace = workspaceID
	s.gotQuery = query
	s.gotCursor = cursor
	s.gotLimit = limit
	return s.page, nil
}

func TestCreateChangeRequestUsesVerifiedContextAndPreservesRemoteSuccess(t *testing.T) {
	repository := Repository{
		ProviderID:      "acme",
		ConnectionScope: "connection-7",
		RepositoryID:    "immutable-repository-9",
		Name:            "tools/widgets",
	}
	created := ChangeRequest{
		Identity: ChangeRequestIdentity{
			ConnectionScope: "connection-7",
			RepositoryID:    "immutable-repository-9",
			Number:          42,
		},
		URL: "https://code.example/tools/widgets/changes/42",
	}
	attached := &attachedRepositoryStub{repository: repository}
	changes := &changeRequestStub{created: created}
	associations := &associationStub{linkErr: errors.New("state store unavailable")}
	extension := &Extension{
		ProviderID:           "acme",
		AttachedRepositories: attached,
		ChangeRequests:       changes,
		Associations:         associations,
	}

	response, err := extension.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: ActionChangeRequestsCreate,
		Context: pluginsdk.VerifiedActionContext{
			ActorID:      "actor-1",
			WorkspaceID:  "workspace-1",
			TaskID:       "task-1",
			RepositoryID: "host-repository-1",
			SessionID:    "session-1",
			HeadBranch:   "verified/head",
		},
		Body: []byte(`{
			"title":"Add provider hooks",
			"description":"Small and neutral",
			"destination":"main",
			"draft":true,
			"source_branch":"attacker/source",
			"workspace_id":"attacker-workspace",
			"repository_id":"attacker-repository"
		}`),
	})
	require.NoError(t, err)
	require.Equal(t, int32(0), int32(response.Status), "zero is the pluginsdk-compatible 200 status")
	require.Equal(t, "workspace-1", attached.got.WorkspaceID)
	require.Equal(t, "host-repository-1", attached.got.RepositoryID)
	require.Equal(t, "verified/head", changes.gotSource)
	require.Equal(t, repository, changes.gotRepository)
	require.Equal(t, "Add provider hooks", changes.gotInput.Title)
	require.Equal(t, "main", changes.gotInput.Destination)
	require.True(t, changes.gotInput.Draft)
	require.Equal(t, "task-1", associations.taskID)
	require.Equal(t, created.Identity, associations.linked)

	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body, &body))
	require.Equal(t, created.URL, body["url"])
	require.Equal(t, false, body["linked"])
	require.Equal(t, "task association could not be saved", body["association_error"])
}

func TestCreateChangeRequestDoesNotPersistIncompleteRemoteIdentity(t *testing.T) {
	changes := &changeRequestStub{created: ChangeRequest{
		URL: "https://code.example/tools/widgets/changes/42",
		Identity: ChangeRequestIdentity{
			ConnectionScope: "connection-7",
			Number:          42,
		},
	}}
	associations := &associationStub{}
	extension := &Extension{
		ProviderID:           "acme",
		AttachedRepositories: &attachedRepositoryStub{repository: Repository{RepositoryID: "repository-9"}},
		ChangeRequests:       changes,
		Associations:         associations,
	}

	response, err := extension.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: ActionChangeRequestsCreate,
		Context: pluginsdk.VerifiedActionContext{
			WorkspaceID: "workspace-1", TaskID: "task-1", RepositoryID: "host-repository-1",
			SessionID: "session-1", HeadBranch: "verified/head",
		},
		Body: []byte(`{"title":"Created remotely","destination":"main"}`),
	})
	require.NoError(t, err)
	require.Empty(t, associations.taskID, "an incomplete provider identity must never be stored")

	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body, &body))
	require.Equal(t, changes.created.URL, body["url"])
	require.Equal(t, false, body["linked"])
	require.Equal(t, "task association could not be saved", body["association_error"])
}

func TestRepositoryListUsesServerSearchAndReturnsOpaqueCursor(t *testing.T) {
	catalog := &repositoryListStub{
		scope: "connection-7",
		page: RepositoryPage{
			Repositories: []Repository{{
				ProviderID:      "acme",
				ConnectionScope: "connection-7",
				RepositoryID:    "immutable-repository-9",
				Name:            "tools/widgets",
			}},
			Next: RepositoryCursor{Remote: "remote-page-2", AfterRepositoryID: "immutable-repository-9"},
		},
	}
	extension := &Extension{ProviderID: "acme", Repositories: catalog}

	response, err := extension.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: ActionRepositoriesList,
		Context:   pluginsdk.VerifiedActionContext{WorkspaceID: "workspace-1"},
		Body:      []byte(`{"query":"widgets","limit":1000}`),
	})
	require.NoError(t, err)
	require.Equal(t, "workspace-1", catalog.gotWorkspace)
	require.Equal(t, "widgets", catalog.gotQuery)
	require.Equal(t, RepositoryCursor{}, catalog.gotCursor)
	require.Equal(t, 100, catalog.gotLimit)

	var body struct {
		Repositories []Repository `json:"repositories"`
		NextCursor   string       `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(response.Body, &body))
	require.Equal(t, catalog.page.Repositories, body.Repositories)
	require.NotEmpty(t, body.NextCursor)
	require.NotContains(t, body.NextCursor, "remote-page-2", "browser cursor must stay opaque")
}

func TestRepositoryListConsumesOpaqueCursor(t *testing.T) {
	catalog := &repositoryListStub{
		scope: "connection-7",
		page: RepositoryPage{
			Next: RepositoryCursor{Remote: "remote-page-2", AfterRepositoryID: "immutable-repository-9"},
		},
	}
	extension := &Extension{ProviderID: "acme", Repositories: catalog}
	first, err := extension.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: ActionRepositoriesList,
		Context:   pluginsdk.VerifiedActionContext{WorkspaceID: "workspace-1"},
		Body:      []byte(`{"query":"widgets","limit":20}`),
	})
	require.NoError(t, err)
	var firstBody struct {
		NextCursor string `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(first.Body, &firstBody))

	catalog.page = RepositoryPage{}
	requestBody, err := json.Marshal(map[string]any{
		"query": "widgets", "cursor": firstBody.NextCursor, "limit": 20,
	})
	require.NoError(t, err)
	_, err = extension.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: ActionRepositoriesList,
		Context:   pluginsdk.VerifiedActionContext{WorkspaceID: "workspace-1"},
		Body:      requestBody,
	})
	require.NoError(t, err)
	require.Equal(t, 2, catalog.calls)
	require.Equal(t, RepositoryCursor{
		Remote:            "remote-page-2",
		AfterRepositoryID: "immutable-repository-9",
	}, catalog.gotCursor)
}

func TestRepositoryListRejectsCursorFromDifferentQuery(t *testing.T) {
	catalog := &repositoryListStub{
		scope: "connection-7",
		page: RepositoryPage{
			Next: RepositoryCursor{Remote: "remote-page-2", AfterRepositoryID: "immutable-repository-9"},
		},
	}
	extension := &Extension{ProviderID: "acme", Repositories: catalog}
	first, err := extension.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: ActionRepositoriesList,
		Context:   pluginsdk.VerifiedActionContext{WorkspaceID: "workspace-1"},
		Body:      []byte(`{"query":"widgets","limit":20}`),
	})
	require.NoError(t, err)
	var firstBody struct {
		NextCursor string `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(first.Body, &firstBody))
	requestBody, err := json.Marshal(map[string]any{
		"query": "different", "cursor": firstBody.NextCursor, "limit": 20,
	})
	require.NoError(t, err)

	_, err = extension.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: ActionRepositoriesList,
		Context:   pluginsdk.VerifiedActionContext{WorkspaceID: "workspace-1"},
		Body:      requestBody,
	})
	require.ErrorContains(t, err, "cursor does not match query")
	require.Equal(t, 1, catalog.calls, "mismatched cursor must be rejected before provider I/O")
}

func TestRepositoryListRejectsCursorAfterConnectionChanges(t *testing.T) {
	catalog := &repositoryListStub{
		scope: "connection-7",
		page: RepositoryPage{
			Next: RepositoryCursor{Remote: "remote-page-2", AfterRepositoryID: "immutable-repository-9"},
		},
	}
	extension := &Extension{ProviderID: "acme", Repositories: catalog}
	first, err := extension.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: ActionRepositoriesList,
		Context:   pluginsdk.VerifiedActionContext{WorkspaceID: "workspace-1"},
		Body:      []byte(`{"query":"widgets","limit":20}`),
	})
	require.NoError(t, err)
	var firstBody struct {
		NextCursor string `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(first.Body, &firstBody))
	catalog.scope = "replacement-connection"
	requestBody, err := json.Marshal(map[string]any{
		"query": "widgets", "cursor": firstBody.NextCursor, "limit": 20,
	})
	require.NoError(t, err)

	_, err = extension.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: ActionRepositoriesList,
		Context:   pluginsdk.VerifiedActionContext{WorkspaceID: "workspace-1"},
		Body:      requestBody,
	})
	require.ErrorContains(t, err, "cursor does not match connection")
	require.Equal(t, 1, catalog.calls)
}

func TestLinkResolvesReferenceInVerifiedWorkspaceAndStoresImmutableIdentity(t *testing.T) {
	identity := ChangeRequestIdentity{
		ConnectionScope: "connection-7",
		RepositoryID:    "immutable-repository-9",
		Number:          42,
	}
	changes := &changeRequestStub{resolved: ChangeRequest{
		Identity: identity,
		Title:    "Provider-neutral hooks",
		URL:      "https://code.example/tools/widgets/changes/42",
	}}
	associations := &associationStub{}
	extension := &Extension{
		ProviderID:     "acme",
		ChangeRequests: changes,
		Associations:   associations,
	}

	_, err := extension.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: ActionChangeRequestsLink,
		Context: pluginsdk.VerifiedActionContext{
			WorkspaceID: "workspace-1",
			TaskID:      "task-1",
		},
		Body: []byte(`{"reference":"tools/widgets#42","workspace_id":"attacker-workspace"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "workspace-1", changes.gotWorkspace)
	require.Equal(t, "tools/widgets#42", changes.gotReference)
	require.Equal(t, "task-1", associations.taskID)
	require.Equal(t, identity, associations.linked)
}

func TestUnlinkUsesCompleteIdentityInsteadOfReviewKey(t *testing.T) {
	associations := &associationStub{}
	extension := &Extension{ProviderID: "acme", Associations: associations}

	_, err := extension.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: ActionChangeRequestsUnlink,
		Context: pluginsdk.VerifiedActionContext{
			WorkspaceID: "workspace-1",
			TaskID:      "task-1",
		},
		Body: []byte(`{
			"review_key":"mutable/repository/path#42",
			"connection_scope":"connection-7",
			"repository_id":"immutable-repository-9",
			"number":42
		}`),
	})
	require.NoError(t, err)
	require.Equal(t, "task-1", associations.unlinkTaskID)
	require.Equal(t, ChangeRequestIdentity{
		ConnectionScope: "connection-7",
		RepositoryID:    "immutable-repository-9",
		Number:          42,
	}, associations.unlinked)
}

func TestInspectURLUsesVerifiedWorkspaceAndBindsProviderIdentity(t *testing.T) {
	inspected := &Repository{
		ProviderID:      "untrusted-result-provider",
		ProviderHost:    "code.example",
		ConnectionScope: "connection-7",
		RepositoryID:    "immutable-repository-9",
		OwnerOrProject:  "tools",
		Name:            "widgets",
		CloneURL:        "https://code.example/tools/widgets.git",
	}
	details := &repositoryDetailsStub{inspected: inspected}
	extension := &Extension{ProviderID: "acme", RepositoryDetails: details}

	response, err := extension.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: ActionRepositoriesInspect,
		Context:   pluginsdk.VerifiedActionContext{WorkspaceID: "workspace-1"},
		Body:      []byte(`{"url":"https://code.example/tools/widgets","workspace_id":"attacker-workspace"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "workspace-1", details.gotWorkspace)
	require.Equal(t, "https://code.example/tools/widgets", details.gotURL)

	var body struct {
		Repository *Repository `json:"repository"`
	}
	require.NoError(t, json.Unmarshal(response.Body, &body))
	require.NotNil(t, body.Repository)
	require.Equal(t, "acme", body.Repository.ProviderID)
	require.Equal(t, inspected.RepositoryID, body.Repository.RepositoryID)
}

func TestBranchesResolveStoredImmutableIdentityBeforeProviderIO(t *testing.T) {
	resolved := Repository{
		ProviderID:      "acme",
		ConnectionScope: "connection-7",
		RepositoryID:    "immutable-repository-9",
		Name:            "tools/widgets",
		CloneURL:        "https://code.example/tools/widgets.git",
	}
	details := &repositoryDetailsStub{
		resolved: resolved,
		branches: []Branch{{Name: "main", Commit: "abc123", IsDefault: true}},
	}
	extension := &Extension{ProviderID: "acme", RepositoryDetails: details}

	response, err := extension.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: ActionRepositoriesBranches,
		Context:   pluginsdk.VerifiedActionContext{WorkspaceID: "workspace-1"},
		Body: []byte(`{"repository":{
			"provider_id":"attacker-provider",
			"provider_scope":"connection-7",
			"provider_repository_id":"immutable-repository-9",
			"clone_url":"https://attacker.example/other.git"
		}}`),
	})
	require.NoError(t, err)
	require.Equal(t, "workspace-1", details.gotWorkspace)
	require.Equal(t, RepositoryIdentity{
		ConnectionScope: "connection-7",
		RepositoryID:    "immutable-repository-9",
	}, details.gotIdentity)
	require.Equal(t, resolved, details.gotBranchRepo, "provider I/O receives server-resolved repository")

	var body struct {
		Branches []Branch `json:"branches"`
	}
	require.NoError(t, json.Unmarshal(response.Body, &body))
	require.Equal(t, details.branches, body.Branches)
}

func TestCancelledActionStopsBeforeProviderIO(t *testing.T) {
	catalog := &repositoryListStub{scope: "connection-7"}
	extension := &Extension{ProviderID: "acme", Repositories: catalog}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := extension.HandleAction(ctx, &pluginsdk.PluginActionRequest{
		ActionKey: ActionRepositoriesList,
		Context:   pluginsdk.VerifiedActionContext{WorkspaceID: "workspace-1"},
		Body:      []byte(`{"query":"widgets"}`),
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 0, catalog.calls)
}

func TestReviewRefreshUsesVerifiedTaskAndReturnsSemanticStatus(t *testing.T) {
	reviews := []ReviewSummary{{
		ProviderID:          "acme",
		ReviewKey:           "tools/widgets#42",
		Title:               "Provider-neutral hooks",
		URL:                 "https://code.example/tools/widgets/changes/42",
		ConnectionScope:     "connection-7",
		RepositoryID:        "immutable-repository-9",
		ChangeRequestNumber: 42,
		State:               "open",
		TaskStatus: &ReviewTaskStatus{
			Number:        42,
			State:         "open",
			PipelineState: "failure",
			Checks: []ReviewTaskStatusCheck{{
				ID: "build", Label: "Build", State: "failure", Detail: "Tests failed",
			}},
		},
	}}
	reader := &reviewReaderStub{reviews: reviews}
	extension := &Extension{ProviderID: "acme", Reviews: reader}

	response, err := extension.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: ActionChangeRequestsGet,
		Context: pluginsdk.VerifiedActionContext{
			WorkspaceID: "workspace-1",
			TaskID:      "task-1",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "workspace-1", reader.gotWorkspace)
	require.Equal(t, "task-1", reader.gotTask)
	var body struct {
		Reviews []ReviewSummary `json:"reviews"`
	}
	require.NoError(t, json.Unmarshal(response.Body, &body))
	require.Equal(t, reviews, body.Reviews)
}

func TestAssociationRefreshReturnsCompleteImmutableIdentities(t *testing.T) {
	associations := []ReviewAssociation{{
		ProviderID:          "untrusted-result-provider",
		TaskID:              "task-1",
		ReviewKey:           "mutable/path#42",
		ConnectionScope:     "connection-7",
		RepositoryID:        "immutable-repository-9",
		ChangeRequestNumber: 42,
	}}
	reader := &reviewReaderStub{associations: associations}
	extension := &Extension{ProviderID: "acme", Reviews: reader}

	response, err := extension.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: ActionChangeRequestAssociations,
		Context:   pluginsdk.VerifiedActionContext{WorkspaceID: "workspace-1"},
	})
	require.NoError(t, err)
	var body struct {
		Associations []ReviewAssociation `json:"associations"`
	}
	require.NoError(t, json.Unmarshal(response.Body, &body))
	require.Equal(t, "acme", body.Associations[0].ProviderID)
	require.Equal(t, associations[0].ConnectionScope, body.Associations[0].ConnectionScope)
	require.Equal(t, associations[0].RepositoryID, body.Associations[0].RepositoryID)
	require.Equal(t, associations[0].ChangeRequestNumber, body.Associations[0].ChangeRequestNumber)
}
