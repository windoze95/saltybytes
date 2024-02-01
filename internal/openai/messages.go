package openai

import (
	"fmt"

	openai "github.com/sashabaranov/go-openai"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/util"
)

// processExistingRecipeHistoryEntries processes existing recipe history messages and returns a slice of chat completion messages
func processExistingRecipeHistoryEntries(historyIn []models.RecipeHistoryEntry) ([]openai.ChatCompletionMessage, error) {
	var messagesOut []openai.ChatCompletionMessage

	for _, entryIn := range historyIn {
		// Serialize the recipe history message
		argumentJSON, err := util.SerializeToJSONStringWithBuffer(entryIn.Response)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize chat completion message: %v", err)
		}

		// Build the message stream
		switch entryIn.Type {
		case models.RecipeTypeManualEntry:
			// Manual type entry is a special case where we want to simulate a revision of the recipe for context.
			messagesOut = append(messagesOut, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleUser,
				Content: "The following response from you is a simulated response containing the current revision of the recipe.",
			})
		default:
			messagesOut = append(messagesOut, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleUser,
				Content: entryIn.Prompt,
			})
		}

		messagesOut = append(messagesOut, openai.ChatCompletionMessage{
			Role: openai.ChatMessageRoleAssistant,
			FunctionCall: &openai.FunctionCall{
				Name:      "create_recipe",
				Arguments: argumentJSON,
			},
		})
	}

	return messagesOut, nil
}

// createRecipeDefRequest creates a multi-message chat completion message with the provided user prompt and image URL.
func createUserMultiMsgVision(userPrompt, imageURL string) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{
		Role: openai.ChatMessageRoleUser,
		MultiContent: []openai.ChatMessagePart{
			{
				Type: openai.ChatMessagePartTypeText,
				Text: userPrompt,
			},
			{
				Type: openai.ChatMessagePartTypeImageURL,
				ImageURL: &openai.ChatMessageImageURL{
					URL:    imageURL,
					Detail: openai.ImageURLDetailHigh,
				},
			},
		},
	}
}

// createSysMsg creates a chat completion message with the provided system prompt.
func createSysMsg(sysPrompt string) openai.ChatCompletionMessage {
	return createMsg(openai.ChatMessageRoleSystem, sysPrompt)
}

// createUserMsg creates a chat completion message with the provided user prompt.
func createUserMsg(userPrompt string) openai.ChatCompletionMessage {
	return createMsg(openai.ChatMessageRoleUser, userPrompt)
}

// createMsg creates a chat completion message with the provided role and prompt.
func createMsg(role string, prompt string) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: prompt,
	}
}
