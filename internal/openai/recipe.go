package openai

import (
	"errors"
	"os"

	openai "github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
	"github.com/windoze95/saltybytes-api/internal/models"
)

type FunctionCallArgument struct {
	models.RecipeDef
	Summary string `json:"recipe_summary"`
}

// createRecipeDefRequest creates a chat completion request for a recipe definition based on the chat completion messages.
func createRecipeDefRequest(chatCompletionMessages []openai.ChatCompletionMessage, isRegen bool) (*openai.ChatCompletionRequest, error) {
	// Validate the chat completion messages
	if len(chatCompletionMessages) == 0 {
		return nil, errors.New("failed to create recipe chat completion: chatCompletionMessages is empty")
	}

	// Define the summary prompt
	var summaryPrompt string
	if isRegen {
		summaryPrompt = os.Getenv("SUMMARIZE_RECIPE_CHANGES")
	} else {
		summaryPrompt = os.Getenv("SUMMARIZE_RECIPE") // "Please summarize the recipe."
	}

	// Define the function call recipe definition parameters
	recipeDefParams := map[string]jsonschema.Definition{
		"title": {
			Type:        jsonschema.String,
			Description: "Title of the recipe or meal",
		},
		"ingredients": {
			Type:        jsonschema.Array,
			Description: "List of ingredients used in the recipe",
			Items: &jsonschema.Definition{
				Type: jsonschema.Object,
				Properties: map[string]jsonschema.Definition{
					"name":   {Type: jsonschema.String, Description: "Name of the ingredient, do not include unit or amount in this field"},
					"unit":   {Type: jsonschema.String, Description: "Unit for the ingredient, comply with UnitSystem specified.", Enum: []string{"pieces", "tsp", "tbsp", "fl oz", "cup", "pt", "qt", "gal", "oz", "lb", "mL", "L", "mg", "g", "kg", "pinch", "dash", "drop", "bushel"}},
					"amount": {Type: jsonschema.Number, Description: "Amount of the ingredient"},
				},
			},
		},
		"instructions": {
			Type:        jsonschema.Array,
			Description: "Steps to prepare the recipe (no numbering)",
			Items:       &jsonschema.Definition{Type: jsonschema.String},
		},
		"cook_time": {
			Type:        jsonschema.Number,
			Description: "Total time to prepare the recipe(s) in minutes",
		},
		"image_prompt": {
			Type:        jsonschema.String,
			Description: "Prompt to generate an image for the recipe, this should be relavent to the recipe and not the user request",
		},
		// "unit_system": {
		// 	Type:        jsonschema.String,
		// 	Enum:        []string{models.USCustomaryText, models.MetricText},
		// 	Description: "Unit system to be used (us customary or metric)",
		// },
		"hashtags": {
			Type:        jsonschema.Array,
			Description: "Provide a lengthy and thorough list (ten or more) of hashtags relevant to the recipe, not the prompting. Alphanumeric characters only. No '#'. Exclude terms like 'recipe', 'homemade', 'DIY', or similar words, as they are understood to be implied. Omit the '#' symbol. Use camelCase formatting if more than one word (if it starts with a letter, the first letter is always lowercase). Note that the following example hashtags are for categorization purposes only and should not influence the actual recipe or ingredients: Instead of specific terms like 'grillSeason', 'grassFedBeef', and 'beetrootKetchup', use more general terms that could apply to similar dishes like 'grilled', 'grill', 'grassFed', 'burgers', 'beef', 'beetroot', 'ketchup'.",
			Items:       &jsonschema.Definition{Type: jsonschema.String},
		},
		"linked_recipe_suggestions": {
			Type:        jsonschema.Array,
			Description: "Provide a list of recipe suggestions(just the titles) based on: 1. Homemade versions of store-bought ingredients used in this recipe. 2. Something that would pair well with this recipe.",
			Items:       &jsonschema.Definition{Type: jsonschema.String},
		},
		"recipe_summary": {
			Type:        jsonschema.String,
			Description: summaryPrompt,
		},
	}

	// Define the function for use in the API call
	functionDef := openai.FunctionDefinition{
		Name: "create_recipe",
		Parameters: jsonschema.Definition{
			Type:       jsonschema.Object,
			Properties: recipeDefParams,
			// Required: []string{"unit_system"},
		},
	}

	// Define list of functions for use in the chat completion request
	functions := []openai.FunctionDefinition{functionDef}

	// Create and return the chat completion request
	return &openai.ChatCompletionRequest{
		Model:            openai.GPT4TurboPreview,
		Messages:         chatCompletionMessages,
		Temperature:      0.7,
		TopP:             0.9,
		N:                1,
		Stream:           false,
		PresencePenalty:  0.2,
		FrequencyPenalty: 0,
		Functions:        functions,
		FunctionCall: &openai.FunctionCall{
			Name: functionDef.Name,
		},
	}, nil
}
