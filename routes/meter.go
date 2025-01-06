package routes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	openai "github.com/sashabaranov/go-openai"
)

type Response struct {
	Indicator int    `json:"indicator"`
	Comment   string `json:"comment"`
	Review    string `json:"review"`
}

var (
	cache      Response
	cacheMutex sync.Mutex
	cacheTime  time.Time
	cacheTTL   = 15 * time.Minute
)

func SetupMeterRoute(r *gin.Engine) {
	r.POST("/meter", MeterHandler)
}

func MeterHandler(c *gin.Context) {
	cacheMutex.Lock()
	if time.Since(cacheTime) < cacheTTL {
		c.JSON(http.StatusOK, cache)
		cacheMutex.Unlock()
		return
	}
	cacheMutex.Unlock()
	ex, err := os.Executable()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to determine executable path"})
		return
	}
	promptFilePath := filepath.Join(filepath.Dir(ex), "prompt.xml")
	prompt, err := os.ReadFile(promptFilePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read prompt file"})
		return
	}

	// Parse the request body
	var articles []map[string]interface{}
	if err := c.BindJSON(&articles); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if len(articles) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No articles provided"})
		return
	}

	eventTitles := ""
	for _, article := range articles {
		if title, ok := article["title"].(string); ok {
			eventTitles += title + "\n"
		}
	}

	// Retrieve OpenAI API key from environment
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "OpenAI API key is not set"})
		return
	}

	// Set up OpenAI client
	client := openai.NewClient(apiKey)

	// Create a completion request
	req := openai.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []openai.ChatCompletionMessage{
			{Role: "system", Content: string(prompt)},
			{Role: "user", Content: fmt.Sprintf("Here are the recent events: %s", eventTitles)},
		},
		MaxTokens: 1000,
	}

	// Get the response from OpenAI
	resp, err := client.CreateChatCompletion(c, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"indicator": 42, "comment": "An error occurred"})
		return
	}

	// Parse the response
	content := resp.Choices[0].Message.Content

	// Extract content between ```json and ```
	if matches := regexp.MustCompile("(?s)```json(.*?)```").FindStringSubmatch(content); len(matches) > 1 {
		content = matches[1]
	}

	var result Response
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"indicator": 42, "comment": "Failed to parse response"})
		return
	}

	// Update the cache
	cacheMutex.Lock()
	cache = result
	cacheTime = time.Now()
	cacheMutex.Unlock()

	// Return the result
	c.JSON(http.StatusOK, result)
}
