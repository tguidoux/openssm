package handlers

import (
	"context"

	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

type addTagsInput struct {
	ResourceType string      `json:"ResourceType"`
	ResourceID   string      `json:"ResourceId"`
	Tags         []types.Tag `json:"Tags"`
}

type removeTagsInput struct {
	ResourceType string   `json:"ResourceType"`
	ResourceID   string   `json:"ResourceId"`
	TagKeys      []string `json:"TagKeys"`
}

type listTagsInput struct {
	ResourceType string `json:"ResourceType"`
	ResourceID   string `json:"ResourceId"`
}

type listTagsOutput struct {
	TagList []types.Tag `json:"TagList"`
}

func registerTags(h *Handler, s *tags.Service) {
	registerNoOutput(h, "AddTagsToResource", func(ctx context.Context, in *addTagsInput) error {
		return s.Add(in.ResourceType, in.ResourceID, in.Tags)
	})
	registerNoOutput(h, "RemoveTagsFromResource", func(ctx context.Context, in *removeTagsInput) error {
		return s.Remove(in.ResourceType, in.ResourceID, in.TagKeys)
	})
	register(h, "ListTagsForResource", func(ctx context.Context, in *listTagsInput) (*listTagsOutput, error) {
		list, err := s.List(in.ResourceType, in.ResourceID)
		if err != nil {
			return nil, err
		}
		return &listTagsOutput{TagList: list}, nil
	})
}
