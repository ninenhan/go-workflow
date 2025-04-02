package fn

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/bwmarrin/snowflake"
	"strings"
)

func IsEmpty(s string) bool {
	return s == "" || strings.TrimSpace(s) == ""
}

// InitSnowflake 初始化 Snowflake 节点
func InitSnowflake(nodeID int64) (*snowflake.Node, error) {
	node, err := snowflake.NewNode(nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize snowflake node: %v", err)
	}
	return node, nil
}

func GenerateShortID() (string, error) {
	node, err := InitSnowflake(1)
	if err != nil {
		return "", err
	}
	id := node.Generate().Int64()
	// 生成 8 字节随机数
	rnd := make([]byte, 8)
	_, _ = rand.Read(rnd)
	rndStr := hex.EncodeToString(rnd)[0:10] // 取 10 位 HEX，打散
	// 计算 SHA-256 哈希，增加唯一性
	input := fmt.Sprintf("%s%x", rndStr, id)
	hash := sha256.Sum256([]byte(input))
	// 取 SHA-256 前 6 字节，转换为 Base64
	shortID := base64.RawURLEncoding.EncodeToString(hash[:8]) // Base64（URL 安全版）
	// 固定 8 位，并添加 `=` 作为 Padding
	return fmt.Sprintf("%-8s", shortID)[:8], nil
}

//func test() {
//	var wg sync.WaitGroup
//	var mu sync.Mutex
//	seen := make(map[string]struct{})
//
//	// 并发数和测试总数
//	concurrency := 2000         // 并发 goroutines 数
//	totalRequests := 20_000_000 // 总共生成的 ID 数
//
//	// 开始并发测试
//	startTime := time.Now()
//	fmt.Println("开始并发生成 ID...")
//	var duplicated []string
//	for i := 0; i < concurrency; i++ {
//		wg.Add(1)
//		go func() {
//			defer wg.Done()
//			for j := 0; j < totalRequests/concurrency; j++ {
//				id, _ := GenerateShortID()
//
//				// 检查是否重复
//				mu.Lock()
//				if _, exists := seen[id]; exists {
//					fmt.Println("❌ 发现重复 ID:", id)
//					duplicated = append(duplicated, id)
//				}
//				fmt.Println(" ID:", id)
//				seen[id] = struct{}{}
//				mu.Unlock()
//			}
//		}()
//	}
//
//	wg.Wait()
//	elapsedTime := time.Since(startTime)
//
//	fmt.Printf("✅ 生成 %d 个 ID，发现重复 %d\n", totalRequests, len(duplicated))
//	fmt.Printf("⏳ 耗时: %v\n", elapsedTime)
//
//}
