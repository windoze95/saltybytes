package openai

import (
	"errors"
	"fmt"

	openai "github.com/sashabaranov/go-openai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/util"
)

// generateNewVisionImportRecipe generates a new recipe from an image.
func generateRecipeWithImportVision(r *RecipeManager) error {
	// New recipe, there shouldn't be a history
	if r.RecipeHistoryEntries != nil || len(r.RecipeHistoryEntries) > 0 {
		return errors.New("RecipeHistoryEntries was not empty")
	}

	sysPromptTemplate := r.Cfg.OpenaiPrompts.GenNewVisionImportArgsSys
	userPromptTemplate := r.Cfg.OpenaiPrompts.GenNewVisionImportArgsUser
	sysPrompt := r.Cfg.OpenaiPrompts.FillSysPrompt(sysPromptTemplate, r.UnitSystem, r.Requirements)
	userPrompt := r.Cfg.OpenaiPrompts.FillUserPrompt(userPromptTemplate, r.UserPrompt)
	chatCompletionMessages := []openai.ChatCompletionMessage{
		createSysMsg(sysPrompt),
		createUserMultiMsgVision(userPrompt, r.VisionImageURL),
	}

	// Generate the unformatted recipe
	visionReplyMessage, err := createVisionChatCompletion(chatCompletionMessages, r.Cfg)
	if err != nil {
		return fmt.Errorf("failed to create chat completion: %v", err)
	}

	// Add the chat completion message to the history
	chatCompletionMessages = append(chatCompletionMessages, *visionReplyMessage)
	chatCompletionMessages = append(chatCompletionMessages, createUserMsg("Proceed."))

	// Create the request
	recipeDefRequest, err := createRecipeDefRequest(chatCompletionMessages, false)
	if err != nil {
		return err
	}

	// Generate the recipe def
	resp, err := createChatCompletionWithRetry(recipeDefRequest, r.Cfg)
	if err != nil {
		return fmt.Errorf("failed to create chat completion: %v", err)
	}

	// Get the recipe def
	recipeDefJSON := resp.Choices[0].Message.FunctionCall.Arguments
	if len(resp.Choices) == 0 || recipeDefJSON == "" {
		return errors.New("OpenAI API returned an empty message")
	}

	// Deserialize the recipe def
	var functionCallArgument FunctionCallArgument
	if err = util.DeserializeFromJSONString(recipeDefJSON, &functionCallArgument); err != nil {
		return fmt.Errorf("failed to deserialize FunctionCallArgument: %v", err)
	}

	// Set the recipe def
	r.RecipeDef = &functionCallArgument.RecipeDef

	// Set the next history message
	r.NextRecipeHistoryEntry = models.RecipeHistoryEntry{
		Prompt:   r.UserPrompt,
		Response: &functionCallArgument.RecipeDef,
		Type:     models.RecipeTypeImportVision,
	}

	return nil
}

// createVisionChatCompletion generates a chat completion with vision from the provided chat completion messages.
func createVisionChatCompletion(chatCompletionMessages []openai.ChatCompletionMessage, cfg *config.Config) (*openai.ChatCompletionMessage, error) {
	// Validate the chat completion messages
	if chatCompletionMessages == nil {
		return nil, errors.New("chatCompletionMessages is nil")
	}

	// Perform the chat completion
	resp, err := createChatCompletionWithRetry(&openai.ChatCompletionRequest{
		Model:            openai.GPT4VisionPreview,
		Messages:         chatCompletionMessages,
		Temperature:      0.7,
		TopP:             0.9,
		N:                1,
		Stream:           false,
		PresencePenalty:  0.2,
		FrequencyPenalty: 0,
	}, cfg)
	if err != nil {
		return nil, err
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return nil, errors.New("OpenAI API returned an empty message")
	}

	return &resp.Choices[0].Message, nil
}
