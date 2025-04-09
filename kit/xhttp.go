package xhttp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type XRequest struct {
	Id      string              `json:"id"`
	Url     string              `json:"url"`
	Method  string              `json:"method"`
	Headers map[string][]string `json:"headers"`
	Body    any                 `json:"body"`
}

// HandleStreamResponseUnTyped 处理流式响应
func HandleStreamResponseUnTyped(ctx context.Context, resp *http.Response, isOpenAI bool, ch chan<- any) error {
	defer func() {
		close(ch) // 确保只有在流数据处理完后才关闭 Channel
	}()
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		// 检查流结束符
		// 检查是否是流结束标志
		if line == "[DONE]" || line == "data: [DONE]" {
			break
		}
		// 处理以 "data:" 开头的内容
		if len(line) > 6 && line[:6] == "data: " {
			line = line[6:] // 去掉 "data: " 前缀
			var data any
			if isOpenAI {
				var streamResp ChatGPTStreamResponse
				if err := json.Unmarshal([]byte(line), &streamResp); err != nil {
					return fmt.Errorf("解析流数据失败: %w", err)
				}
				data = streamResp
			} else {
				data = line
			}
			select {
			case ch <- data: // 将内容发送到 Channel
			case <-ctx.Done(): // 如果 context 被取消，则退出
				return ctx.Err()
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取流数据失败: %w", err)
	}
	return nil
}

// HandleNonStreamResponseUnTyped 处理非流式响应
func HandleNonStreamResponseUnTyped(ctx context.Context, resp *http.Response, isOpenAI bool, ch chan<- any) error {
	defer func() {
		close(ch) // 确保只有在流数据处理完后才关闭 Channel
	}()
	var data any
	if isOpenAI {
		var response ChatGPTResponse
		err := json.NewDecoder(resp.Body).Decode(&response)
		if err != nil {
			return fmt.Errorf("解析非流式响应失败: %v", err)
		}
		data = response
	} else {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		data = body
	}
	//// 获取所有的内容
	select {
	case ch <- data: // 将内容发送到 Channel
	case <-ctx.Done(): // 如果 context 被取消，则退出
		return ctx.Err()
	}
	return nil
}

// HandlerHttpWithChannel HTTP 请求处理函数
func HandlerHttpWithChannel(xRequest XRequest, isPreCooked bool, ch chan<- any) error {
	// 序列化请求体
	body, err := json.Marshal(xRequest.Body)
	if err != nil {
		return fmt.Errorf("序列化请求体失败: %v", err)
	}
	// 创建 HTTP 请求
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, xRequest.Url, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("创建请求失败: %v", err)
	}
	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	for key, values := range xRequest.Headers {
		req.Header.Set(key, strings.Join(values, ","))
	}
	// 创建 HTTP 客户端并发送请求
	client := &http.Client{
		Timeout: 60 * time.Second, // 设置超时时间
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("发送请求失败: %v", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Printf("关闭 Body 失败: %v\n", err)
		}
	}(resp.Body)

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body) // 读取错误信息
		return fmt.Errorf("请求失败，状态码: %d，响应: %s", resp.StatusCode, string(bodyBytes))
	}
	// 读取 Content-Type 确定响应类型
	contentType := resp.Header.Get("Content-Type")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	// 处理 Stream 和非 Stream 两种模式
	if strings.HasPrefix(contentType, "text/event-stream") {
		// Stream 模式：逐行读取数据流
		return HandleStreamResponseUnTyped(ctx, resp, isPreCooked, ch)
	} else {
		// 非 Stream 模式：直接解析完整的 JSON 响应
		return HandleNonStreamResponseUnTyped(ctx, resp, isPreCooked, ch)
	}
}
