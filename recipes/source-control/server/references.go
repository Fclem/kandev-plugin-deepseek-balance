package sourcecontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kandev/kandev/pkg/pluginsdk"
)

const (
	referencePurposeSearch     = "search"
	referencePurposeSubmission = "submission"
)

// ReferenceService owns provider-specific canonical identity validation.
// Authorize must perform live provider access checks; it must not trust or
// cache a preceding Search result.
type ReferenceService interface {
	Search(context.Context, string, string, int) ([]pluginsdk.EntityReferenceCandidate, error)
	Authorize(context.Context, string, string, map[string]any) (bool, error)
}

var (
	_ pluginsdk.EntityReferenceSearcher   = (*Extension)(nil)
	_ pluginsdk.EntityReferenceAuthorizer = (*Extension)(nil)
)

func (e *Extension) SearchEntityReferences(ctx context.Context, request *pluginsdk.SearchEntityReferencesRequest) (*pluginsdk.SearchEntityReferencesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil || request.Source != e.ReferenceSource || strings.TrimSpace(request.WorkspaceID) == "" || e.References == nil {
		return nil, errors.New("source-control recipe: invalid reference search")
	}
	limit := int(request.Limit)
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	candidates, err := e.References.Search(ctx, request.WorkspaceID, strings.TrimSpace(request.Query), limit)
	if err != nil {
		return nil, fmt.Errorf("source-control recipe: search references: %w", err)
	}
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return &pluginsdk.SearchEntityReferencesResponse{Candidates: candidates}, nil
}

func (e *Extension) AuthorizeEntityReference(ctx context.Context, request *pluginsdk.AuthorizeEntityReferenceRequest) (*pluginsdk.AuthorizeEntityReferenceResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil || request.Source != e.ReferenceSource || strings.TrimSpace(request.WorkspaceID) == "" || e.References == nil ||
		(request.Purpose != referencePurposeSearch && request.Purpose != referencePurposeSubmission) {
		return &pluginsdk.AuthorizeEntityReferenceResponse{
			Allowed: false,
			Reason:  "reference source or purpose is invalid",
		}, nil
	}
	allowed, err := e.References.Authorize(ctx, request.WorkspaceID, request.Purpose, request.Reference)
	if err != nil {
		return nil, fmt.Errorf("source-control recipe: authorize reference: %w", err)
	}
	if !allowed {
		return &pluginsdk.AuthorizeEntityReferenceResponse{
			Allowed: false,
			Reason:  "reference is no longer available",
		}, nil
	}
	return &pluginsdk.AuthorizeEntityReferenceResponse{Allowed: true}, nil
}
