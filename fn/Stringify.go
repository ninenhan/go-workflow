package fn

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/bwmarrin/snowflake"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"path/filepath"
	"regexp"
	"strconv"
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

func GenerateShortIDWithDigits(digits uint8) (string, error) {
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
	shortID := base64.RawURLEncoding.EncodeToString(hash[:digits]) // Base64（URL 安全版）
	// 固定 8 位，并添加 `=` 作为 Padding
	result := fmt.Sprintf("%-8s", shortID)[:digits]
	result = regexp.MustCompile(`\s+`).ReplaceAllString(result, "")
	return result, nil
}

func GenerateShortID() (string, error) {
	return GenerateShortIDWithDigits(8)
}

func Uuid() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

func MustJSON(v any) datatypes.JSON {
	b, _ := json.Marshal(v)
	return b
}

func UniqueStrings(input []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(input))
	for _, v := range input {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			result = append(result, v)
		}
	}
	return result
}

func GetSuffix(str string) string {
	return filepath.Ext(str)
}

func NumericStringify(v any) string {
	switch val := v.(type) {
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64) // 'f' 表示非科学计数法
	case *float64:
		if val == nil {
			return ""
		}
		return strconv.FormatFloat(*val, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(val, 10)
	case *int64:
		if val == nil {
			return ""
		}
		return strconv.FormatInt(*val, 10)
	case FlexibleFloat64:
		return fmt.Sprintf("%v", strconv.FormatFloat(float64(val), 'f', -1, 64)) // "123.456" ✅ 精度不变
	case FlexibleInt64, FlexibleInt8:
		return fmt.Sprintf("%d", val)
	default:
		return fmt.Sprintf("%v", v)
	}
}
