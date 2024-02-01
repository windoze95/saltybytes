package openai

import (
	"errors"
	"fmt"

	openai "github.com/sashabaranov/go-openai"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/util"
)

// GenerateNewRecipe generates a new recipe.
func generateRecipeWithChat(r *RecipeManager) error {
	// New recipe, there shouldn't be a history
	if r.RecipeHistoryEntries != nil || len(r.RecipeHistoryEntries) > 0 {
		return errors.New("RecipeHistoryEntries was not empty")
	}

	// Build the chat completion message stream
	sysPromptTemplate := r.Cfg.OpenaiPrompts.GenNewRecipeSys
	// userPromptTemplate := r.Cfg.OpenaiPrompts.GenNewRecipeUser
	sysPrompt := r.Cfg.OpenaiPrompts.FillSysPrompt(sysPromptTemplate, r.UnitSystem, r.Requirements)
	// userPrompt := r.Cfg.OpenaiPrompts.FillUserPrompt(userPromptTemplate, r.UserPrompt)
	chatCompletionMessages := []openai.ChatCompletionMessage{
		createSysMsg(sysPrompt),
		createUserMsg(r.UserPrompt),
	}

	// Create the request
	recipeDefRequest, err := createRecipeDefRequest(chatCompletionMessages, false)
	if err != nil {
		return err
	}

	// Perform the chat completion
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
		Summary:  functionCallArgument.Summary,
		Type:     models.RecipeTypeChat,
	}

	return nil
}
