package api

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadEnvFile 加载 .env 文件（支持多行 PEM 密钥）
// 格式：KEY="value" 或 KEY=value
// 多行值：KEY="-----BEGIN...\n...\n-----END..."
func LoadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var currentKey string
	var currentVal []string
	inMultiline := false

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// 跳过空行和注释
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if inMultiline {
			currentVal = append(currentVal, line)
			if strings.Contains(trimmed, "-----END") {
				inMultiline = false
				fullVal := strings.Join(currentVal, "\n")
				// 去掉首尾引号
				fullVal = strings.Trim(fullVal, `"'`)
				os.Setenv(currentKey, fullVal)
			}
			continue
		}

		if strings.Contains(trimmed, "=") {
			parts := strings.SplitN(trimmed, "=", 2)
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])

			// 检查是否是多行值的开始（支持 *** 或 -----BEGIN）
			if (strings.HasPrefix(val, `"-----BEGIN`) || strings.HasPrefix(val, `'-----BEGIN`) ||
				strings.HasPrefix(val, `"***`) || strings.HasPrefix(val, `'***`)) &&
				!strings.Contains(val, "-----END") {
				inMultiline = true
				currentKey = key
				currentVal = []string{val}
				continue
			}

			// 单行值，去掉引号
			val = strings.Trim(val, `"'`)
			// 处理 \n 转义为实际换行（用于 PEM 密钥）
			val = strings.ReplaceAll(val, `\n`, "\n")
			os.Setenv(key, val)
		}
	}

	if inMultiline {
		return fmt.Errorf("多行值未结束，缺少 -----END")
	}

	return scanner.Err()
}
