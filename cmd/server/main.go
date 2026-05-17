package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"poc-mcp/internal/app/core/todos"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var core *todos.Core

func main() {
	core = todos.New()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "cul-todo",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "todos-list",
		Description: "Get list of what Ucul need to do",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: new(false),
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			OpenWorldHint:   new(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input InputList) (
		*mcp.CallToolResult, any, error,
	) {
		list, err := core.List()
		if err != nil {
			return nil, nil, err
		}

		res, err := json.Marshal(list)
		if err != nil {
			return nil, nil, err
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: string(res),
				},
			},
		}, nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "todos-add",
		Description: "Create todo for Ucul to do",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: new(false),
			ReadOnlyHint:    false,
			IdempotentHint:  false,
			OpenWorldHint:   new(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input InputAdd) (
		*mcp.CallToolResult, any, error,
	) {
		res, err := core.Add(input.Text)
		if err != nil {
			return nil, nil, err
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: fmt.Sprintf("Todo %d created", res.ID),
				},
			},
		}, nil, nil
	})

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "todos-toggle",
			Description: "Toggle the mark for Ucul to do as done or not done using ID for toggling the to do",
			Annotations: &mcp.ToolAnnotations{
				DestructiveHint: new(false),
				ReadOnlyHint:    false,
				IdempotentHint:  false,
				OpenWorldHint:   new(true),
			},
		},
		func(ctx context.Context, req *mcp.CallToolRequest, input InputToggle) (
			*mcp.CallToolResult, any, error,
		) {
			res, err := core.ToggleDone(input.ID)
			if err != nil {
				return nil, nil, errors.Join(
					err, fmt.Errorf("todo with ID of %d is not found", input.ID))
			}

			marker := func() string {
				if res.Done {
					return "done"
				}
				return "not done"
			}()

			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{
						Text: fmt.Sprintf(
							"Todo with ID of %d is toggled to <%s>",
							res.ID,
							marker,
						),
					},
				},
			}, nil, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "todos-remove",
			Description: "Remove Ucul to do by its ID",
			Annotations: &mcp.ToolAnnotations{
				DestructiveHint: new(true),
				ReadOnlyHint:    false,
				IdempotentHint:  false,
				OpenWorldHint:   new(true),
			},
		},
		func(ctx context.Context, req *mcp.CallToolRequest, input InputRemove) (
			*mcp.CallToolResult, any, error,
		) {
			err := core.Remove(input.ID)
			if err != nil {
				return nil, nil, errors.Join(
					err, fmt.Errorf("todo with ID of %d is not found", input.ID))
			}

			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{
						Text: fmt.Sprintf("Todo with ID of %d removed", input.ID),
					},
				},
			}, nil, nil
		},
	)

	err := server.Run(context.Background(), &mcp.StdioTransport{})
	if err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
}

type InputList struct{}

type InputAdd struct {
	Text string `json:"text" jsonschema:"Description of what Ucul need to do"`
}

type InputToggle struct {
	ID int64 `json:"id" jsonschema:"ID of the to do to be toggled"`
}

type InputRemove struct {
	ID int64 `json:"id" jsonschema:"ID of the to do to be removed"`
}
