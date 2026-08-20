// Package sourcecontrol is a provider-neutral recipe. It contains only the
// Kandev boundary: a real plugin supplies provider-specific implementations of
// the narrow interfaces below and delegates its optional SDK methods here.
package sourcecontrol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kandev/kandev/pkg/pluginsdk"
)

const (
	ActionRepositoriesList          = "repositories.list"
	ActionRepositoriesInspect       = "repositories.inspect"
	ActionRepositoriesBranches      = "repositories.branches"
	ActionChangeRequestsCreate      = "change_requests.create"
	ActionChangeRequestsGet         = "change_requests.get"
	ActionChangeRequestsLink        = "change_requests.link"
	ActionChangeRequestsUnlink      = "change_requests.unlink"
	ActionChangeRequestAssociations = "change_requests.associations"
)

// Repository holds credential-free provider identity and display/routing data.
// RepositoryID must be the provider's immutable identifier; Name is display only.
type Repository struct {
	ProviderID      string `json:"provider_id"`
	ProviderHost    string `json:"provider_host"`
	ConnectionScope string `json:"provider_scope"`
	RepositoryID    string `json:"repository_id"`
	OwnerOrProject  string `json:"owner_or_project"`
	Name            string `json:"name"`
	CloneURL        string `json:"clone_url"`
	DefaultBranch   string `json:"default_branch,omitempty"`
}

type RepositoryIdentity struct {
	ConnectionScope string `json:"connection_scope"`
	RepositoryID    string `json:"repository_id"`
}

type Branch struct {
	Name      string `json:"name"`
	Commit    string `json:"commit,omitempty"`
	IsDefault bool   `json:"is_default,omitempty"`
}

// ChangeRequestIdentity is the complete immutable association key.
type ChangeRequestIdentity struct {
	ConnectionScope string `json:"connection_scope"`
	RepositoryID    string `json:"repository_id"`
	Number          int64  `json:"number"`
}

type ChangeRequest struct {
	Identity ChangeRequestIdentity `json:"identity"`
	Title    string                `json:"title,omitempty"`
	URL      string                `json:"url"`
}

type CreateChangeRequestInput struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Destination string `json:"destination"`
	Draft       bool   `json:"draft"`
}

type ReviewTaskStatusCheck struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	URL    string `json:"url,omitempty"`
}

type ReviewTaskReview struct {
	State     string `json:"state"`
	Approved  int    `json:"approved"`
	Required  int    `json:"required,omitempty"`
	Requested int    `json:"requested,omitempty"`
}

type ReviewTaskStatus struct {
	Number             int64                   `json:"number"`
	State              string                  `json:"state"`
	PipelineState      string                  `json:"pipeline_state"`
	Checks             []ReviewTaskStatusCheck `json:"checks"`
	Review             *ReviewTaskReview       `json:"review,omitempty"`
	UnresolvedComments int                     `json:"unresolved_comments,omitempty"`
	UpdatedAt          int64                   `json:"updated_at,omitempty"` // Unix milliseconds.
}

type ReviewSummary struct {
	ProviderID          string            `json:"provider_id"`
	ReviewKey           string            `json:"review_key"`
	Title               string            `json:"title"`
	URL                 string            `json:"url"`
	ConnectionScope     string            `json:"connection_scope"`
	RepositoryID        string            `json:"repository_id"`
	ChangeRequestNumber int64             `json:"change_request_number"`
	State               string            `json:"state"`
	TaskStatus          *ReviewTaskStatus `json:"task_status,omitempty"`
}

type ReviewAssociation struct {
	ProviderID          string `json:"provider_id"`
	TaskID              string `json:"task_id"`
	ReviewKey           string `json:"review_key"`
	ConnectionScope     string `json:"connection_scope"`
	RepositoryID        string `json:"repository_id"`
	ChangeRequestNumber int64  `json:"change_request_number"`
}

type RepositoryCursor struct {
	Remote            string
	AfterRepositoryID string
}

type RepositoryPage struct {
	Repositories []Repository
	Next         RepositoryCursor
}

type RepositoryLister interface {
	ConnectionScope(context.Context, string) (string, error)
	List(context.Context, string, string, RepositoryCursor, int) (RepositoryPage, error)
}

type RepositoryDetails interface {
	Inspect(context.Context, string, string) (*Repository, error)
	Resolve(context.Context, string, RepositoryIdentity) (Repository, error)
	ListBranches(context.Context, string, Repository) ([]Branch, error)
}

// AttachedRepositoryResolver must resolve through Host Tasks/Repositories APIs.
// Browser descriptors are never authority for create operations.
type AttachedRepositoryResolver interface {
	ResolveAttached(context.Context, pluginsdk.VerifiedActionContext) (Repository, error)
}

type ChangeRequestService interface {
	ResolveReference(context.Context, string, string) (ChangeRequest, error)
	Create(context.Context, Repository, string, CreateChangeRequestInput) (ChangeRequest, error)
}

type AssociationStore interface {
	Link(context.Context, string, ChangeRequestIdentity) error
	Unlink(context.Context, string, ChangeRequestIdentity) error
}

type ReviewReader interface {
	ForTask(context.Context, string, string) ([]ReviewSummary, error)
	Associations(context.Context, string) ([]ReviewAssociation, error)
}

// Extension implements optional plugin SDK surfaces. Embed or delegate to it
// from the concrete value passed to pluginsdk.Serve.
type Extension struct {
	ProviderID           string
	ReferenceSource      string
	Repositories         RepositoryLister
	RepositoryDetails    RepositoryDetails
	AttachedRepositories AttachedRepositoryResolver
	ChangeRequests       ChangeRequestService
	Associations         AssociationStore
	Reviews              ReviewReader
	References           ReferenceService
}

var _ pluginsdk.ActionHandler = (*Extension)(nil)

func (e *Extension) HandleAction(ctx context.Context, request *pluginsdk.PluginActionRequest) (*pluginsdk.PluginActionResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, errors.New("source-control recipe: action request is required")
	}
	switch request.ActionKey {
	case ActionRepositoriesList:
		return e.listRepositories(ctx, request)
	case ActionRepositoriesInspect:
		return e.inspectRepository(ctx, request)
	case ActionRepositoriesBranches:
		return e.listBranches(ctx, request)
	case ActionChangeRequestsCreate:
		return e.createChangeRequest(ctx, request)
	case ActionChangeRequestsGet:
		return e.getReviews(ctx, request)
	case ActionChangeRequestAssociations:
		return e.getAssociations(ctx, request)
	case ActionChangeRequestsLink:
		return e.linkChangeRequest(ctx, request)
	case ActionChangeRequestsUnlink:
		return e.unlinkChangeRequest(ctx, request)
	default:
		return nil, fmt.Errorf("source-control recipe: unsupported action %q", request.ActionKey)
	}
}

func (e *Extension) getAssociations(ctx context.Context, request *pluginsdk.PluginActionRequest) (*pluginsdk.PluginActionResponse, error) {
	if strings.TrimSpace(request.Context.WorkspaceID) == "" || e.Reviews == nil {
		return nil, errors.New("source-control recipe: association refresh requires a verified workspace")
	}
	associations, err := e.Reviews.Associations(ctx, request.Context.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("source-control recipe: refresh workspace associations: %w", err)
	}
	for i := range associations {
		identity := ChangeRequestIdentity{
			ConnectionScope: associations[i].ConnectionScope,
			RepositoryID:    associations[i].RepositoryID,
			Number:          associations[i].ChangeRequestNumber,
		}
		if strings.TrimSpace(associations[i].TaskID) == "" {
			return nil, errors.New("source-control recipe: review association task is required")
		}
		if err := validateChangeRequestIdentity(identity); err != nil {
			return nil, err
		}
		associations[i].ProviderID = e.ProviderID
	}
	return jsonResponse(map[string]any{"associations": associations})
}

func (e *Extension) getReviews(ctx context.Context, request *pluginsdk.PluginActionRequest) (*pluginsdk.PluginActionResponse, error) {
	if strings.TrimSpace(request.Context.WorkspaceID) == "" || strings.TrimSpace(request.Context.TaskID) == "" || e.Reviews == nil {
		return nil, errors.New("source-control recipe: review refresh requires verified workspace and task context")
	}
	reviews, err := e.Reviews.ForTask(ctx, request.Context.WorkspaceID, request.Context.TaskID)
	if err != nil {
		return nil, fmt.Errorf("source-control recipe: refresh task reviews: %w", err)
	}
	for i := range reviews {
		identity := ChangeRequestIdentity{
			ConnectionScope: reviews[i].ConnectionScope,
			RepositoryID:    reviews[i].RepositoryID,
			Number:          reviews[i].ChangeRequestNumber,
		}
		if err := validateChangeRequestIdentity(identity); err != nil {
			return nil, err
		}
		reviews[i].ProviderID = e.ProviderID
	}
	return jsonResponse(map[string]any{"reviews": reviews})
}

func (e *Extension) listBranches(ctx context.Context, request *pluginsdk.PluginActionRequest) (*pluginsdk.PluginActionResponse, error) {
	if strings.TrimSpace(request.Context.WorkspaceID) == "" || e.RepositoryDetails == nil {
		return nil, errors.New("source-control recipe: branch list requires a verified workspace")
	}
	var input struct {
		Repository struct {
			ConnectionScope      string `json:"provider_scope"`
			RepositoryID         string `json:"repository_id"`
			ProviderRepositoryID string `json:"provider_repository_id"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(request.Body, &input); err != nil {
		return nil, fmt.Errorf("source-control recipe: decode branch list body: %w", err)
	}
	repositoryID := strings.TrimSpace(input.Repository.ProviderRepositoryID)
	if repositoryID == "" {
		repositoryID = strings.TrimSpace(input.Repository.RepositoryID)
	}
	identity := RepositoryIdentity{
		ConnectionScope: strings.TrimSpace(input.Repository.ConnectionScope),
		RepositoryID:    repositoryID,
	}
	if identity.ConnectionScope == "" || identity.RepositoryID == "" {
		return nil, errors.New("source-control recipe: complete repository identity is required")
	}
	repository, err := e.RepositoryDetails.Resolve(ctx, request.Context.WorkspaceID, identity)
	if err != nil {
		return nil, fmt.Errorf("source-control recipe: resolve repository identity: %w", err)
	}
	branches, err := e.RepositoryDetails.ListBranches(ctx, request.Context.WorkspaceID, repository)
	if err != nil {
		return nil, fmt.Errorf("source-control recipe: list branches: %w", err)
	}
	return jsonResponse(map[string]any{"branches": branches})
}

func (e *Extension) inspectRepository(ctx context.Context, request *pluginsdk.PluginActionRequest) (*pluginsdk.PluginActionResponse, error) {
	if strings.TrimSpace(request.Context.WorkspaceID) == "" || e.RepositoryDetails == nil {
		return nil, errors.New("source-control recipe: repository inspection requires a verified workspace")
	}
	var input struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(request.Body, &input); err != nil {
		return nil, fmt.Errorf("source-control recipe: decode repository inspection body: %w", err)
	}
	input.URL = strings.TrimSpace(input.URL)
	if input.URL == "" {
		return nil, errors.New("source-control recipe: repository URL is required")
	}
	repository, err := e.RepositoryDetails.Inspect(ctx, request.Context.WorkspaceID, input.URL)
	if err != nil {
		return nil, fmt.Errorf("source-control recipe: inspect repository URL: %w", err)
	}
	if repository == nil {
		return jsonResponse(map[string]any{"repository": nil})
	}
	bound := *repository
	bound.ProviderID = e.ProviderID
	return jsonResponse(map[string]any{"repository": bound})
}

func (e *Extension) unlinkChangeRequest(ctx context.Context, request *pluginsdk.PluginActionRequest) (*pluginsdk.PluginActionResponse, error) {
	if strings.TrimSpace(request.Context.WorkspaceID) == "" || strings.TrimSpace(request.Context.TaskID) == "" {
		return nil, errors.New("source-control recipe: unlink requires verified workspace and task context")
	}
	if e.Associations == nil {
		return nil, errors.New("source-control recipe: association store is not configured")
	}
	var input struct {
		ConnectionScope string `json:"connection_scope"`
		RepositoryID    string `json:"repository_id"`
		Number          int64  `json:"number"`
	}
	if err := json.Unmarshal(request.Body, &input); err != nil {
		return nil, fmt.Errorf("source-control recipe: decode unlink body: %w", err)
	}
	identity := ChangeRequestIdentity{
		ConnectionScope: strings.TrimSpace(input.ConnectionScope),
		RepositoryID:    strings.TrimSpace(input.RepositoryID),
		Number:          input.Number,
	}
	if err := validateChangeRequestIdentity(identity); err != nil {
		return nil, err
	}
	if err := e.Associations.Unlink(ctx, request.Context.TaskID, identity); err != nil {
		return nil, fmt.Errorf("source-control recipe: unlink task association: %w", err)
	}
	return jsonResponse(map[string]any{"unlinked": true})
}

func (e *Extension) linkChangeRequest(ctx context.Context, request *pluginsdk.PluginActionRequest) (*pluginsdk.PluginActionResponse, error) {
	if strings.TrimSpace(request.Context.WorkspaceID) == "" || strings.TrimSpace(request.Context.TaskID) == "" {
		return nil, errors.New("source-control recipe: link requires verified workspace and task context")
	}
	if e.ChangeRequests == nil || e.Associations == nil {
		return nil, errors.New("source-control recipe: link dependencies are not configured")
	}
	var input struct {
		Reference string `json:"reference"`
	}
	if err := json.Unmarshal(request.Body, &input); err != nil {
		return nil, fmt.Errorf("source-control recipe: decode link body: %w", err)
	}
	input.Reference = strings.TrimSpace(input.Reference)
	if input.Reference == "" {
		return nil, errors.New("source-control recipe: change-request reference is required")
	}
	change, err := e.ChangeRequests.ResolveReference(ctx, request.Context.WorkspaceID, input.Reference)
	if err != nil {
		return nil, fmt.Errorf("source-control recipe: resolve change-request reference: %w", err)
	}
	if err := validateChangeRequestIdentity(change.Identity); err != nil {
		return nil, err
	}
	if err := e.Associations.Link(ctx, request.Context.TaskID, change.Identity); err != nil {
		return nil, fmt.Errorf("source-control recipe: link task association: %w", err)
	}
	return jsonResponse(map[string]any{"linked": true, "change_request": change})
}

func validateChangeRequestIdentity(identity ChangeRequestIdentity) error {
	if strings.TrimSpace(identity.ConnectionScope) == "" || strings.TrimSpace(identity.RepositoryID) == "" || identity.Number <= 0 {
		return errors.New("source-control recipe: change-request identity is incomplete")
	}
	return nil
}

func (e *Extension) listRepositories(ctx context.Context, request *pluginsdk.PluginActionRequest) (*pluginsdk.PluginActionResponse, error) {
	if strings.TrimSpace(request.Context.WorkspaceID) == "" || e.Repositories == nil {
		return nil, errors.New("source-control recipe: repository list requires a verified workspace")
	}
	var input struct {
		Query  string `json:"query"`
		Cursor string `json:"cursor"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(request.Body, &input); err != nil {
		return nil, fmt.Errorf("source-control recipe: decode repository list body: %w", err)
	}
	limit := input.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	scope, err := e.Repositories.ConnectionScope(ctx, request.Context.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("source-control recipe: resolve connection scope: %w", err)
	}
	cursor := RepositoryCursor{}
	if input.Cursor != "" {
		envelope, err := decodeRepositoryCursor(input.Cursor)
		if err != nil {
			return nil, err
		}
		if envelope.Query != strings.TrimSpace(input.Query) {
			return nil, errors.New("source-control recipe: repository cursor does not match query")
		}
		if envelope.ConnectionScope != scope {
			return nil, errors.New("source-control recipe: repository cursor does not match connection")
		}
		cursor = RepositoryCursor{
			Remote:            envelope.Remote,
			AfterRepositoryID: envelope.AfterRepositoryID,
		}
	}
	page, err := e.Repositories.List(ctx, request.Context.WorkspaceID, strings.TrimSpace(input.Query), cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("source-control recipe: list repositories: %w", err)
	}
	result := map[string]any{"repositories": page.Repositories}
	if page.Next.Remote != "" {
		cursor, err := encodeRepositoryCursor(repositoryCursorEnvelope{
			Query:             strings.TrimSpace(input.Query),
			ConnectionScope:   scope,
			Remote:            page.Next.Remote,
			AfterRepositoryID: page.Next.AfterRepositoryID,
		})
		if err != nil {
			return nil, err
		}
		result["next_cursor"] = cursor
	}
	return jsonResponse(result)
}

type repositoryCursorEnvelope struct {
	Query             string `json:"q"`
	ConnectionScope   string `json:"s"`
	Remote            string `json:"c"`
	AfterRepositoryID string `json:"r"`
}

func encodeRepositoryCursor(cursor repositoryCursorEnvelope) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("source-control recipe: encode repository cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeRepositoryCursor(value string) (repositoryCursorEnvelope, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return repositoryCursorEnvelope{}, fmt.Errorf("source-control recipe: invalid repository cursor: %w", err)
	}
	var cursor repositoryCursorEnvelope
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return repositoryCursorEnvelope{}, fmt.Errorf("source-control recipe: invalid repository cursor: %w", err)
	}
	return cursor, nil
}

func (e *Extension) createChangeRequest(ctx context.Context, request *pluginsdk.PluginActionRequest) (*pluginsdk.PluginActionResponse, error) {
	action := request.Context
	if strings.TrimSpace(action.WorkspaceID) == "" || strings.TrimSpace(action.TaskID) == "" ||
		strings.TrimSpace(action.RepositoryID) == "" || strings.TrimSpace(action.SessionID) == "" ||
		strings.TrimSpace(action.HeadBranch) == "" {
		return nil, errors.New("source-control recipe: complete verified task checkout context is required")
	}
	if e.AttachedRepositories == nil || e.ChangeRequests == nil || e.Associations == nil {
		return nil, errors.New("source-control recipe: create dependencies are not configured")
	}

	var input CreateChangeRequestInput
	if err := json.Unmarshal(request.Body, &input); err != nil {
		return nil, fmt.Errorf("source-control recipe: decode create body: %w", err)
	}
	repository, err := e.AttachedRepositories.ResolveAttached(ctx, action)
	if err != nil {
		return nil, fmt.Errorf("source-control recipe: resolve attached repository: %w", err)
	}
	created, err := e.ChangeRequests.Create(ctx, repository, action.HeadBranch, input)
	if err != nil {
		return nil, fmt.Errorf("source-control recipe: create change request: %w", err)
	}
	created.URL = strings.TrimSpace(created.URL)
	if created.URL == "" {
		return nil, errors.New("source-control recipe: provider create response did not include a URL")
	}

	result := map[string]any{
		"url":      created.URL,
		"provider": e.ProviderID,
		"linked":   true,
	}
	if err := validateChangeRequestIdentity(created.Identity); err != nil {
		result["linked"] = false
		result["association_error"] = "task association could not be saved"
		return jsonResponse(result)
	}
	if err := e.Associations.Link(ctx, action.TaskID, created.Identity); err != nil {
		result["linked"] = false
		result["association_error"] = "task association could not be saved"
	}
	return jsonResponse(result)
}

func jsonResponse(value any) (*pluginsdk.PluginActionResponse, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("source-control recipe: encode action response: %w", err)
	}
	return &pluginsdk.PluginActionResponse{
		Body:    body,
		Headers: map[string]string{"Content-Type": "application/json"},
	}, nil
}
