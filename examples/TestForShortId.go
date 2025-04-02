package main

import (
	"fmt"
	"github.com/ninenhan/go-workflow/fn"
	"sync"
	"time"
)

// TestForShortId 生成短号ID
func TestForShortId() {

	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[string]struct{})

	// 并发数和测试总数
	concurrency := 2000        // 并发 goroutines 数
	totalRequests := 2_000_000 // 总共生成的 ID 数

	// 开始并发测试
	startTime := time.Now()
	fmt.Println("开始并发生成 ID...")
	var duplicated []string
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < totalRequests/concurrency; j++ {
				id, _ := fn.GenerateShortID()

				// 检查是否重复
				mu.Lock()
				if _, exists := seen[id]; exists {
					fmt.Println("❌ 发现重复 ID:", id)
					duplicated = append(duplicated, id)
				}
				//fmt.Println(" ID:", id)
				seen[id] = struct{}{}
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	elapsedTime := time.Since(startTime)

	fmt.Printf("✅ 生成 %d 个 ID，发现重复 %d\n", totalRequests, len(duplicated))
	fmt.Printf("⏳ 耗时: %v\n", elapsedTime)

}
