package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

func main() {
	mcpClient := mcp.NewClient(&mcp.Implementation{
		Name:    "mcp-client-go",
		Version: "0.1.0",
	}, nil)

	slog, err := NewLogger()
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}

	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "./server")

	transport := &mcp.CommandTransport{Command: cmd}
	sess, err := mcpClient.Connect(ctx, transport, nil)
	if err != nil {
		log.Fatalf("Failed to connect to server: %v", err)
	}
	defer sess.Close()

	tools, err := sess.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		log.Fatalf("Failed to list tools: %v", err)
	}

	oaiTools := make([]responses.ToolUnionParam, len(tools.Tools))
	for i, tool := range tools.Tools {
		mschema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			log.Fatalf("InputSchema is not a map[string]any")
		}

		ofFunc := responses.FunctionToolParam{
			Name:        tool.Name,
			Description: openai.String(tool.Description),
			Strict:      openai.Bool(true),
			Parameters:  mschema,
		}
		unionParam := responses.ToolUnionParam{
			OfFunction: &ofFunc,
		}

		oaiTools[i] = unionParam
	}

	r := newRunner(slog, sess, openai.NewClient(
		option.WithBaseURL("http://localhost:11434/v1"),
	), oaiTools)

	// question := "What is 1+1?"
	// question := "Is there any Ucul to do that still need to be done?"
	// question := "Add 'Clean up dev server' into Ucul to do list"
	// question := "I already clean up my dev server, can you mark that as done in my to do list? After that, let me know which tasks is done and which is still outstanding?"
	question := "I already clean up my dev server, can you mark that task as done in my to do list? After that, let me know which tasks is done and which is still outstanding?"

	err = r.Run(ctx, question)
	if err != nil {
		log.Fatalf("Failed to run: %s", err)
	}
}

type Runner struct {
	slog *slog.Logger

	mcpSession *mcp.ClientSession

	oaiClient openai.Client
	tools     []responses.ToolUnionParam

	reasoning       shared.ReasoningParam
	maxOutputTokens param.Opt[int64]
	model           string
}

func newRunner(
	slog *slog.Logger,
	mcpSession *mcp.ClientSession,
	oaiClient openai.Client,
	tools []responses.ToolUnionParam,
) *Runner {
	return &Runner{
		slog: slog,

		mcpSession: mcpSession,
		oaiClient:  oaiClient,
		tools:      tools,

		reasoning: shared.ReasoningParam{
			// Effort: openai.ReasoningEffortNone, // Put no reasoning for faster response
			Effort: openai.ReasoningEffortMedium,
		},
		maxOutputTokens: openai.Int(12000),
		model:           "qwen3.5:4b",
	}
}

func (r *Runner) Run(ctx context.Context, question string) error {
	return r.ask(ctx, responses.ResponseInputParam{
		responses.ResponseInputItemParamOfMessage(question, responses.EasyInputMessageRoleUser),
	}, param.Opt[string]{})
}

func (r *Runner) ask(
	ctx context.Context,
	convos responses.ResponseInputParam,
	prevResID param.Opt[string],
) error {
	stream := r.oaiClient.Responses.NewStreaming(ctx, responses.ResponseNewParams{
		Model: r.model,
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: convos,
		},
		PreviousResponseID: prevResID,

		MaxOutputTokens: r.maxOutputTokens,
		Reasoning:       r.reasoning,
		Tools:           r.tools,
	})
	defer stream.Close()

	var outputText string
	var tokenUsed int64
	var currentResponseID param.Opt[string]

	inputList := make(responses.ResponseInputParam, 0)

	for stream.Next() {
		data := stream.Current()

		r.slog.DebugContext(ctx, data.RawJSON())

		switch variant := data.AsAny().(type) {
		case responses.ResponseReasoningSummaryTextDeltaEvent:
			fmt.Print(variant.Delta)
		case responses.ResponseCompletedEvent:
			outputText = variant.Response.OutputText()
			tokenUsed = variant.Response.Usage.TotalTokens
			currentResponseID = openai.String(variant.Response.ID)

			for _, output := range variant.Response.Output {
				switch out := output.AsAny().(type) {
				case responses.ResponseFunctionToolCall:
					inputList = append(inputList,
						responses.ResponseInputItemParamOfFunctionCall(
							out.Arguments,
							out.CallID,
							out.Name,
						),
					)

					fmt.Println()
					fmt.Printf("Calling Tool [%s]\nWith Arguments:\n%s", out.Name, out.Arguments)

					var args map[string]any
					err := json.Unmarshal([]byte(out.Arguments), &args)
					if err != nil {
						return errors.Join(err, errors.New("failed to unmarshal tool args"))
					}

					mcpToolRes, err := r.mcpSession.CallTool(ctx, &mcp.CallToolParams{
						Name:      out.Name,
						Arguments: args,
					})
					if err != nil {
						return errors.Join(err, errors.New("failed to call tool"))
					}

					resultJSON, err := json.Marshal(mcpToolRes)
					if err != nil {
						return errors.Join(err, errors.New("failed to serialize tool result"))
					}

					inputList = append(inputList,
						responses.ResponseInputItemParamOfFunctionCallOutput(
							out.CallID,
							string(resultJSON),
						),
					)

					fmt.Println()
					fmt.Println("Tool called successfully")
				}
			}
		}
	}

	err := stream.Err()
	if err != nil {
		return errors.Join(err, errors.New("failed to read stream"))
	}

	if outputText != "" {
		fmt.Println()
		log.Println("Final Response:", outputText)
		fmt.Println()
		log.Printf("token exhausted: %d", tokenUsed)

		return nil
	}

	return r.ask(ctx, inputList, currentResponseID)
}

func NewLogger() (*slog.Logger, error) {
	fp := filepath.Join("./temp", "log.log")
	f, err := os.OpenFile(fp, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}

	slogHandler := slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})

	return slog.New(slogHandler), nil
}
