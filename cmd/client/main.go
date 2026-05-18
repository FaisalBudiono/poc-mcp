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
	"strings"

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
	// question := "What is capital of Japan?"
	// question := "Is there any Ucul to do that still need to be done?"
	// question := "Add 'Clean up dev server' into Ucul to do list"
	// question := "I already clean up my dev server, can you mark that as done in my to do list? After that, let me know which tasks is done and which is still outstanding?"
	// question := "I marked the clean up my dev server as done before. It actually hasn't done. Can you put that as not done again?"
	// question := "Server apa yang harus aku bersihkan di todo listku?"
	question := "Aku sudah beli susu dan clean up my bedroom. Please mark both of that as done. After that tell me, what other todo list that I need to do?"

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

	oldTools []openai.ChatCompletionToolUnionParam

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
	oldTools := make([]openai.ChatCompletionToolUnionParam, len(tools))
	for i, tool := range tools {
		oldTools[i] = openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        tool.OfFunction.Name,
			Strict:      tool.OfFunction.Strict,
			Description: tool.OfFunction.Description,
			Parameters:  tool.OfFunction.Parameters,
		})
	}
	return &Runner{
		slog: slog,

		mcpSession: mcpSession,
		oaiClient:  oaiClient,
		tools:      tools,

		oldTools: oldTools,

		reasoning: shared.ReasoningParam{
			// Effort: openai.ReasoningEffortNone, // Put no reasoning for faster response
			Effort: openai.ReasoningEffortMedium,
		},
		maxOutputTokens: openai.Int(12000),
		model:           "qwen3.5:4b",
	}
}

func (r *Runner) Run(ctx context.Context, question string) error {
	// return r.askChat(ctx, []openai.ChatCompletionMessageParamUnion{
	// 	openai.UserMessage(question),
	// })

	return r.askResponse(ctx, responses.ResponseInputParam{
		responses.ResponseInputItemParamOfMessage(question, responses.EasyInputMessageRoleUser),
	}, param.Opt[string]{})
}

func (r *Runner) askResponse(
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

	var toolCalls []responses.ResponseFunctionToolCall

	for stream.Next() {
		data := stream.Current()

		r.slog.DebugContext(ctx, data.RawJSON())

		switch variant := data.AsAny().(type) {
		case responses.ResponseReasoningSummaryTextDeltaEvent:
			fmt.Print(variant.Delta)
		case responses.ResponseCompletedEvent:
			outputText = variant.Response.OutputText()
			fmt.Println(outputText)

			tokenUsed = variant.Response.Usage.TotalTokens
			currentResponseID = openai.String(variant.Response.ID)

			for _, output := range variant.Response.Output {
				switch out := output.AsAny().(type) {
				case responses.ResponseFunctionToolCall:
					toolCalls = append(toolCalls, out)
				}
			}
		}
	}

	err := stream.Err()
	if err != nil {
		return errors.Join(err, errors.New("failed to read stream"))
	}

	for _, tc := range toolCalls {
		convos = append(convos,
			responses.ResponseInputItemParamOfFunctionCall(
				tc.Arguments,
				tc.CallID,
				tc.Name,
			),
		)

		fmt.Println()
		fmt.Println()
		fmt.Printf("Calling Tool [%s]\nWith Arguments:\n%s", tc.Name, tc.Arguments)

		var args map[string]any
		err := json.Unmarshal([]byte(tc.Arguments), &args)
		if err != nil {
			return errors.Join(err, errors.New("failed to unmarshal tool args"))
		}

		mcpToolRes, err := r.mcpSession.CallTool(ctx, &mcp.CallToolParams{
			Name:      tc.Name,
			Arguments: args,
		})
		if err != nil {
			return errors.Join(err, errors.New("failed to call tool"))
		}

		resultJSON, err := json.Marshal(mcpToolRes)
		if err != nil {
			return errors.Join(err, errors.New("failed to serialize tool result"))
		}

		convos = append(convos,
			responses.ResponseInputItemParamOfFunctionCallOutput(
				tc.CallID,
				string(resultJSON),
			),
		)

		fmt.Println()
		fmt.Println("Tool called successfully")
		fmt.Println()
	}

	if len(toolCalls) > 0 {
		return r.askResponse(ctx, convos, currentResponseID)
	}

	fmt.Println()
	log.Println("Final Response:", outputText)
	fmt.Println()
	log.Printf("token exhausted: %d", tokenUsed)

	return nil
}

type deltaCustom struct {
	Reasoning string `json:"reasoning"`
}

func (r *Runner) askChat(
	ctx context.Context,
	convos []openai.ChatCompletionMessageParamUnion,
) error {
	stream := r.oaiClient.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Model:    r.model,
		Messages: convos,

		MaxCompletionTokens: r.maxOutputTokens,
		ReasoningEffort:     r.reasoning.Effort,
		Tools:               r.oldTools,
	})
	defer stream.Close()

	var tokenUsed int64
	var outputText strings.Builder
	var finalReason string
	justReachConclusion := true

	var toolCalls []openai.ChatCompletionChunkChoiceDeltaToolCall

	for stream.Next() {
		data := stream.Current()
		tokenUsed = data.Usage.TotalTokens

		r.slog.DebugContext(ctx, data.RawJSON())

		if len(data.Choices) == 0 {
			r.slog.DebugContext(ctx, "skip stream", slog.String("data", data.RawJSON()))
			continue
		}

		for _, c := range data.Choices {
			delta := c.Delta

			var dc deltaCustom
			err := json.Unmarshal([]byte(delta.RawJSON()), &dc)
			if err != nil {
				return errors.Join(err, errors.New("failed to unmarshal delta"))
			}

			if dc.Reasoning != "" {
				fmt.Print(dc.Reasoning)
				continue
			}

			if justReachConclusion {
				fmt.Println()
				fmt.Println()
			}
			justReachConclusion = false

			fmt.Print(delta.Content)

			if len(delta.ToolCalls) > 0 {
				toolCalls = append(toolCalls, delta.ToolCalls...)
			}

			outputText.WriteString(delta.Content)
			finalReason = c.FinishReason
		}
	}

	err := stream.Err()
	if err != nil {
		return errors.Join(err, errors.New("failed to read stream"))
	}

	for _, tc := range toolCalls {
		var args map[string]any
		err := json.Unmarshal([]byte(tc.Function.Arguments), &args)
		if err != nil {
			return errors.Join(err, errors.New("failed to unmarshal tool args"))
		}

		fmt.Println()
		fmt.Printf("Calling Tool [%s]\nWith Arguments:\n%s", tc.Function.Name, tc.Function.Arguments)
		fmt.Println()

		mcpToolRes, err := r.mcpSession.CallTool(ctx, &mcp.CallToolParams{
			Name:      tc.Function.Name,
			Arguments: args,
		})
		if err != nil {
			return errors.Join(err, errors.New("failed to call tool"))
		}

		resultJSON, err := json.Marshal(mcpToolRes)
		if err != nil {
			return errors.Join(err, errors.New("failed to serialize tool result"))
		}

		convos = append(convos, openai.ToolMessage(string(resultJSON), tc.ID))

		fmt.Println("Tool called successfully")
		fmt.Println()
	}

	if len(toolCalls) > 0 {
		fmt.Println()
		log.Println("Final Reason:", finalReason)
		return r.askChat(ctx, convos)
	}

	fmt.Println()
	log.Println("Final Reason:", finalReason)
	log.Println("Final Response:", outputText.String())
	log.Println("Token used:", tokenUsed)

	return nil
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
