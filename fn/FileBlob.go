package fn

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func GzipCompress(input []byte) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, err := w.Write(input)
	if err != nil {
		return nil
	}
	err = w.Close()
	if err != nil {
		return nil
	}
	return b.Bytes()
}

func GzipDecompress(input []byte) []byte {
	b := bytes.NewBuffer(input)
	r, _ := gzip.NewReader(b)
	out, _ := io.ReadAll(r)
	err := r.Close()
	if err != nil {
		return nil
	}
	return out
}

func DownloadToTempDir(url string) (*os.File, error) {
	// 发起 HTTP 请求
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Printf("关闭 Body 失败: %v\n", err)
		}
	}(resp.Body)
	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download file, status code: %d", resp.StatusCode)
	}
	// 创建临时文件
	tmpFile, err := os.CreateTemp("", "tmp_d_*.tmp")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer func(tmpFile *os.File) {
		err := tmpFile.Close()
		if err != nil {
			log.Println(err)
		}
	}(tmpFile)
	// 将响应内容写入临时文件
	_, err = io.Copy(tmpFile, resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to write to temp file: %w", err)
	}
	// 返回临时文件
	return tmpFile, nil
}

func SaveToWAVFile(filename string, data []byte) (*os.File, error) {
	// 获取系统的临时目录路径
	tempDir := os.TempDir()
	// 组合临时文件完整路径
	fullPath := filepath.Join(tempDir, filename)
	log.Println("fullPath :", fullPath)
	file, err := os.Create(fullPath)
	if err != nil {
		return nil, err
	}
	_, err = file.Write(data)
	if err != nil {
		return nil, err
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			log.Println(err)
		}
	}(file)
	return file, nil
}

func SendFileChunks(file *os.File, firstSize int, chunkSize int, ch chan []byte) {
	defer close(ch) // 关闭通道，表示文件发送完成
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			log.Println(err)
		}
	}(file)
	if firstSize > 0 {
		// 比如：读取WAV头部
		header := make([]byte, firstSize)
		if n, err := file.Read(header); err == nil {
			ch <- header[:n]
		}
	}
	chunkSize = Ternary(chunkSize <= 0, 8192, chunkSize)
	buffer := make([]byte, chunkSize)
	for {
		n, err := file.Read(buffer)
		if err == io.EOF {
			// 文件读取完毕，退出循环
			break
		}
		if err != nil {
			log.Println("读取文件出错:", err)
			break
		}
		// 将读取到的块数据发送到 WebSocket 通道
		ch <- buffer[:n]
	}

}

func FileExists(filePath string) bool {
	_, err := os.Stat(filePath)
	return !os.IsNotExist(err)
}
