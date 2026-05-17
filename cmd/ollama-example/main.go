package main

import (
	"context"
	"fmt"
	"log"

	"github.com/openai/openai-go/v3"
	oaioption "github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

func main() {
	ctx := context.Background()
	client := openai.NewClient(
		oaioption.WithBaseURL("http://localhost:11434/v1"),
	)

	question := "What is 1 + 1? this is not a trick question. Just a simple math question."

	stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
		Input: responses.ResponseNewParamsInputUnion{OfString: openai.String(question)},
		Model: "qwen3.5:4b",
		Reasoning: shared.ReasoningParam{
			Effort: openai.ReasoningEffortNone, // Put no reasoning for faster response
			// Effort: openai.ReasoningEffortLow,
		},
	})
	defer stream.Close()

	var completeRes string
	for stream.Next() {
		data := stream.Current()

		switch variant := data.AsAny().(type) {
		case responses.ResponseReasoningSummaryTextDeltaEvent:
			fmt.Print(variant.Delta)
		case responses.ResponseCompletedEvent:
			completeRes = variant.Response.OutputText()
		}

	}

	err := stream.Err()
	if err != nil {
		log.Fatalf("Failed to read stream %#v", err)
	}

	fmt.Println()

	log.Println("Raw Response", completeRes)
}
