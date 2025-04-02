package xhttp

import "strings"

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatGPTRequest ChatGPT API 请求的结构体
type ChatGPTRequest struct {
	Model       string              `json:"model"`
	Messages    []map[string]string `json:"messages"`
	Stream      *bool               `json:"stream"`
	MaxTokens   *int                `json:"max_tokens"`
	Temperature *float32            `json:"temperature"`
	TopP        *float32            `json:"top_p"`
}

// ChatGPTResponse OpenAI API 响应的结构体（非流式）
type ChatGPTResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Usage   Usage  `json:"usage"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason,omitempty"`
	} `json:"choices"`
}

func (c *ChatGPTResponse) GetResponse() string {
	var results []string
	for _, choice := range c.Choices {
		results = append(results, choice.Message.Content)
	}
	return strings.Join(results, "")
}

// ChatGPTStreamResponse OpenAI API 流式响应的结构体
type ChatGPTStreamResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason,omitempty"`
	} `json:"choices"`
}

func (c *ChatGPTStreamResponse) GetResponse() string {
	var results []string
	for _, choice := range c.Choices {
		results = append(results, choice.Delta.Content)
	}
	return strings.Join(results, "")
}
